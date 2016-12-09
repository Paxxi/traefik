package provider

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/BurntSushi/ty/fun"
	"github.com/cenk/backoff"
	"github.com/containous/traefik/job"
	"github.com/containous/traefik/labels"
	"github.com/containous/traefik/log"
	"github.com/containous/traefik/safe"
	"github.com/containous/traefik/types"
	"github.com/containous/traefik/version"
	"github.com/docker/engine-api/client"
	dockertypes "github.com/docker/engine-api/types"
	dockercontainertypes "github.com/docker/engine-api/types/container"
	eventtypes "github.com/docker/engine-api/types/events"
	"github.com/docker/engine-api/types/filters"
	"github.com/docker/engine-api/types/swarm"
	swarmtypes "github.com/docker/engine-api/types/swarm"
	"github.com/docker/go-connections/nat"
	"github.com/docker/go-connections/sockets"
	"github.com/vdemeester/docker-events"
)

const (
	// DockerAPIVersion is a constant holding the version of the Docker API traefik will use
	DockerAPIVersion string = "1.21"
	// SwarmAPIVersion is a constant holding the version of the Docker API traefik will use
	SwarmAPIVersion string = "1.24"
	// SwarmDefaultWatchTime is the duration of the interval when polling docker
	SwarmDefaultWatchTime = 15 * time.Second
)

var _ Provider = (*Docker)(nil)

// Docker holds configurations of the Docker provider.
type Docker struct {
	BaseProvider     `mapstructure:",squash"`
	Endpoint         string     `description:"Docker server endpoint. Can be a tcp or a unix socket endpoint"`
	Domain           string     `description:"Default domain used"`
	TLS              *ClientTLS `description:"Enable Docker TLS support"`
	ExposedByDefault bool       `description:"Expose containers by default"`
	UseBindPortIP    bool       `description:"Use the ip address from the bound port, rather than from the inner network"`
	SwarmMode        bool       `description:"Use Docker on Swarm Mode"`
}

// dockerData holds the need data to the Docker provider
type DockerData struct {
	Name            string
	Labels          map[string]string // List of labels set to container or service
	NetworkSettings NetworkSettings
	Health          string
}

// NetworkSettings holds the networks data to the Docker provider
type NetworkSettings struct {
	NetworkMode dockercontainertypes.NetworkMode
	Ports       nat.PortMap
	Networks    map[string]*NetworkData
}

// Network holds the network data to the Docker provider
type NetworkData struct {
	Name     string
	Addr     string
	Port     int
	Protocol string
	ID       string
}

func (provider *Docker) createClient() (client.APIClient, error) {
	var httpClient *http.Client
	httpHeaders := map[string]string{
		"User-Agent": "Traefik " + version.Version,
	}
	if provider.TLS != nil {
		config, err := provider.TLS.CreateTLSConfig()
		if err != nil {
			return nil, err
		}
		tr := &http.Transport{
			TLSClientConfig: config,
		}
		proto, addr, _, err := client.ParseHost(provider.Endpoint)
		if err != nil {
			return nil, err
		}

		sockets.ConfigureTransport(tr, proto, addr)

		httpClient = &http.Client{
			Transport: tr,
		}

	}
	var version string
	if provider.SwarmMode {
		version = SwarmAPIVersion
	} else {
		version = DockerAPIVersion
	}
	return client.NewClient(provider.Endpoint, version, httpClient, httpHeaders)

}

// Provide allows the provider to provide configurations to traefik
// using the given configuration channel.
func (provider *Docker) Provide(configurationChan chan<- types.ConfigMessage, pool *safe.Pool, constraints types.Constraints) error {
	provider.Constraints = append(provider.Constraints, constraints...)
	// TODO register this routine in pool, and watch for stop channel
	safe.Go(func() {
		operation := func() error {
			var err error

			dockerClient, err := provider.createClient()
			if err != nil {
				log.Errorf("Failed to create a client for docker, error: %s", err)
				return err
			}

			ctx := context.Background()
			version, err := dockerClient.ServerVersion(ctx)
			log.Debugf("Docker connection established with docker %s (API %s)", version.Version, version.APIVersion)
			var dockerDataList []DockerData
			if provider.SwarmMode {
				dockerDataList, err = listServices(ctx, dockerClient)
				if err != nil {
					log.Errorf("Failed to list services for docker swarm mode, error %s", err)
					return err
				}
			} else {
				dockerDataList, err = listContainers(ctx, dockerClient)
				if err != nil {
					log.Errorf("Failed to list containers for docker, error %s", err)
					return err
				}
			}

			configuration := provider.loadDockerConfig(dockerDataList)
			configurationChan <- types.ConfigMessage{
				ProviderName:  "docker",
				Configuration: configuration,
			}
			if provider.Watch {
				ctx, cancel := context.WithCancel(ctx)
				if provider.SwarmMode {
					// TODO: This need to be change. Linked to Swarm events docker/docker#23827
					ticker := time.NewTicker(SwarmDefaultWatchTime)
					pool.Go(func(stop chan bool) {
						for {
							select {
							case <-ticker.C:
								services, err := listServices(ctx, dockerClient)
								if err != nil {
									log.Errorf("Failed to list services for docker, error %s", err)
									return
								}
								configuration := provider.loadDockerConfig(services)
								if configuration != nil {
									configurationChan <- types.ConfigMessage{
										ProviderName:  "docker",
										Configuration: configuration,
									}
								}

							case <-stop:
								ticker.Stop()
								cancel()
								return
							}
						}
					})

				} else {
					pool.Go(func(stop chan bool) {
						for {
							select {
							case <-stop:
								cancel()
								return
							}
						}
					})
					f := filters.NewArgs()
					f.Add("type", "container")
					options := dockertypes.EventsOptions{
						Filters: f,
					}
					eventHandler := events.NewHandler(events.ByAction)
					startStopHandle := func(m eventtypes.Message) {
						log.Debugf("Docker event received %+v", m)
						containers, err := listContainers(ctx, dockerClient)
						if err != nil {
							log.Errorf("Failed to list containers for docker, error %s", err)
							// Call cancel to get out of the monitor
							cancel()
							return
						}
						configuration := provider.loadDockerConfig(containers)
						if configuration != nil {
							configurationChan <- types.ConfigMessage{
								ProviderName:  "docker",
								Configuration: configuration,
							}
						}
					}
					eventHandler.Handle("start", startStopHandle)
					eventHandler.Handle("die", startStopHandle)
					eventHandler.Handle("health_status: healthy", startStopHandle)
					eventHandler.Handle("health_status: unhealthy", startStopHandle)
					eventHandler.Handle("health_status: starting", startStopHandle)

					errChan := events.MonitorWithHandler(ctx, dockerClient, options, eventHandler)
					if err := <-errChan; err != nil {
						return err
					}
				}
			}
			return nil
		}
		notify := func(err error, time time.Duration) {
			log.Errorf("Docker connection error %+v, retrying in %s", err, time)
		}
		err := backoff.RetryNotify(operation, job.NewBackOff(backoff.NewExponentialBackOff()), notify)
		if err != nil {
			log.Errorf("Cannot connect to docker server %+v", err)
		}
	})

	return nil
}

func (provider *Docker) loadDockerConfig(containersInspected []DockerData) *types.Configuration {
	var DockerFuncMap = template.FuncMap{
		"getBackend":                  provider.getBackend,
		"getIPAddress":                provider.getIPAddress,
		"getPort":                     provider.getPort,
		"getWeight":                   provider.getWeight,
		"getDomain":                   provider.getDomain,
		"getProtocol":                 provider.getProtocol,
		"getPassHostHeader":           provider.getPassHostHeader,
		"getPriority":                 provider.getPriority,
		"getEntryPoints":              provider.getEntryPoints,
		"getFrontendRule":             provider.getFrontendRule,
		"hasCircuitBreakerLabel":      provider.hasCircuitBreakerLabel,
		"getCircuitBreakerExpression": provider.getCircuitBreakerExpression,
		"hasLoadBalancerLabel":        provider.hasLoadBalancerLabel,
		"getLoadBalancerMethod":       provider.getLoadBalancerMethod,
		"hasMaxConnLabels":            provider.hasMaxConnLabels,
		"getMaxConnAmount":            provider.getMaxConnAmount,
		"getMaxConnExtractorFunc":     provider.getMaxConnExtractorFunc,
		"getSticky":                   provider.getSticky,
		"replace":                     replace,
	}

	// filter containers
	filteredContainers := fun.Filter(func(container DockerData) bool {
		return provider.containerFilter(container)
	}, containersInspected).([]DockerData)

	frontends := map[string][]DockerData{}
	backends := map[string]DockerData{}
	servers := map[string][]DockerData{}
	for _, container := range filteredContainers {
		frontendName := provider.getFrontendName(container)
		frontends[frontendName] = append(frontends[frontendName], container)
		backendName := provider.getBackend(container)
		backends[backendName] = container
		servers[backendName] = append(servers[backendName], container)
	}

	templateObjects := struct {
		Containers []DockerData
		Frontends  map[string][]DockerData
		Backends   map[string]DockerData
		Servers    map[string][]DockerData
		Domain     string
	}{
		filteredContainers,
		frontends,
		backends,
		servers,
		provider.Domain,
	}

	configuration, err := provider.getConfiguration("templates/docker.tmpl", DockerFuncMap, templateObjects)
	if err != nil {
		log.Error(err)
	}
	return configuration
}

func (provider *Docker) hasCircuitBreakerLabel(container DockerData) bool {
	return labels.HasCircuitBreakerLabel(container.Labels)
}

func (provider *Docker) hasLoadBalancerLabel(container DockerData) bool {
	return labels.HasLoadBalancerLabel(container.Labels)
}

func (provider *Docker) hasMaxConnLabels(container DockerData) bool {
	return labels.HasMaxConnLabels(container.Labels)
}

func (provider *Docker) getCircuitBreakerExpression(container DockerData) string {
	return labels.GetCircuitBreakerExpression(container.Labels)
}

func (provider *Docker) getLoadBalancerMethod(container DockerData) string {
	return labels.GetLoadBalancerMethod(container.Labels)
}

func (provider *Docker) getMaxConnAmount(container DockerData) int64 {
	return labels.GetMaxConnAmount(container.Labels)
}

func (provider *Docker) getMaxConnExtractorFunc(container DockerData) string {
	return labels.GetMaxConnExtractorFunc(container.Labels)
}

func (provider *Docker) containerFilter(container DockerData) bool {
	_, err := strconv.Atoi(container.Labels["traefik.port"])
	if len(container.NetworkSettings.Ports) == 0 && err != nil {
		log.Debugf("Filtering container without port and no traefik.port label %s", container.Name)
		return false
	}

	if !isContainerEnabled(container, provider.ExposedByDefault) {
		log.Debugf("Filtering disabled container %s", container.Name)
		return false
	}

	constraintTags := strings.Split(container.Labels["traefik.tags"], ",")
	if ok, failingConstraint := provider.MatchConstraints(constraintTags); !ok {
		if failingConstraint != nil {
			log.Debugf("Container %v pruned by '%v' constraint", container.Name, failingConstraint.String())
		}
		return false
	}

	if container.Health != "" && container.Health != "healthy" {
		log.Debugf("Filtering unhealthy or starting container %s", container.Name)
		return false
	}

	return true
}

func (provider *Docker) getFrontendName(container DockerData) string {
	return labels.GetFrontendName(container.Labels,
		"Host:"+provider.getSubDomain(container.Name)+"."+provider.Domain)
}

// GetFrontendRule returns the frontend rule for the specified container, using
// it's label. It returns a default one (Host) if the label is not present.
func (provider *Docker) getFrontendRule(container DockerData) string {
	return labels.GetFrontendName(container.Labels,
		"Host:"+provider.getSubDomain(container.Name)+"."+provider.Domain)
}

func (provider *Docker) getBackend(container DockerData) string {
	return labels.GetBackend(container.Labels, container.Name)
}

func (provider *Docker) getIPAddress(container DockerData) string {
	label, err := labels.GetLabel(container.Labels, "traefik.docker.network")
	if err == nil && label != "" {
		networkSettings := container.NetworkSettings
		if networkSettings.Networks != nil {
			network := networkSettings.Networks[label]
			if network != nil {
				return network.Addr
			}
		}
	}

	// If net==host, quick n' dirty, we return 127.0.0.1
	// This will work locally, but will fail with swarm.
	if "host" == container.NetworkSettings.NetworkMode {
		return "127.0.0.1"
	}

	if provider.UseBindPortIP {
		port := provider.getPort(container)
		for netport, portBindings := range container.NetworkSettings.Ports {
			if string(netport) == port+"/TCP" || string(netport) == port+"/UDP" {
				for _, p := range portBindings {
					return p.HostIP
				}
			}
		}
	}

	for _, network := range container.NetworkSettings.Networks {
		return network.Addr
	}
	return ""
}

func (provider *Docker) getPort(container DockerData) string {
	if label := labels.GetPort(container.Labels); len(label) > 0 {
		return label
	}

	for key := range container.NetworkSettings.Ports {
		return key.Port()
	}
	return ""
}

func (provider *Docker) getWeight(container DockerData) string {
	return labels.GetWeight(container.Labels)
}

func (provider *Docker) getSticky(container DockerData) string {
	return labels.GetSticky(container.Labels)
}

func (provider *Docker) getDomain(container DockerData) string {
	return labels.GetDomain(container.Labels, provider.Domain)
}

func (provider *Docker) getProtocol(container DockerData) string {
	return labels.GetProtocol(container.Labels)
}

func (provider *Docker) getPassHostHeader(container DockerData) string {
	return labels.GetPassHostHeader(container.Labels)
}

func (provider *Docker) getPriority(container DockerData) string {
	return labels.GetPriority(container.Labels)
}

func (provider *Docker) getEntryPoints(container DockerData) []string {
	return labels.GetEntryPoints(container.Labels)
}

func isContainerEnabled(container DockerData, exposedByDefault bool) bool {
	return labels.IsContainerEnabled(container.Labels, exposedByDefault)
}

func listContainers(ctx context.Context, dockerClient client.ContainerAPIClient) ([]DockerData, error) {
	containerList, err := dockerClient.ContainerList(ctx, dockertypes.ContainerListOptions{})
	if err != nil {
		return []DockerData{}, err
	}
	containersInspected := []DockerData{}

	// get inspect containers
	for _, container := range containerList {
		containerInspected, err := dockerClient.ContainerInspect(ctx, container.ID)
		if err != nil {
			log.Warnf("Failed to inspect container %s, error: %s", container.ID, err)
		} else {
			dockerData := parseContainer(containerInspected)
			containersInspected = append(containersInspected, dockerData)
		}
	}
	return containersInspected, nil
}

func parseContainer(container dockertypes.ContainerJSON) DockerData {
	dockerData := DockerData{
		NetworkSettings: NetworkSettings{},
	}

	if container.ContainerJSONBase != nil {
		dockerData.Name = container.ContainerJSONBase.Name

		if container.ContainerJSONBase.HostConfig != nil {
			dockerData.NetworkSettings.NetworkMode = container.ContainerJSONBase.HostConfig.NetworkMode
		}

		if container.State != nil && container.State.Health != nil {
			dockerData.Health = container.State.Health.Status
		}
	}

	if container.Config != nil && container.Config.Labels != nil {
		dockerData.Labels = container.Config.Labels
	}

	if container.NetworkSettings != nil {
		if container.NetworkSettings.Ports != nil {
			dockerData.NetworkSettings.Ports = container.NetworkSettings.Ports
		}
		if container.NetworkSettings.Networks != nil {
			dockerData.NetworkSettings.Networks = make(map[string]*NetworkData)
			for name, containerNetwork := range container.NetworkSettings.Networks {
				dockerData.NetworkSettings.Networks[name] = &NetworkData{
					ID:   containerNetwork.NetworkID,
					Name: name,
					Addr: containerNetwork.IPAddress,
				}
			}
		}

	}

	return dockerData
}

// Escape beginning slash "/", convert all others to dash "-"
func (provider *Docker) getSubDomain(name string) string {
	return strings.Replace(strings.TrimPrefix(name, "/"), "/", "-", -1)
}

func listServices(ctx context.Context, dockerClient client.APIClient) ([]DockerData, error) {
	serviceList, err := dockerClient.ServiceList(ctx, dockertypes.ServiceListOptions{})
	if err != nil {
		return []DockerData{}, err
	}
	networkListArgs := filters.NewArgs()
	networkListArgs.Add("driver", "overlay")

	networkList, err := dockerClient.NetworkList(ctx, dockertypes.NetworkListOptions{Filters: networkListArgs})

	networkMap := make(map[string]*dockertypes.NetworkResource)
	if err != nil {
		log.Debug("Failed to network inspect on client for docker, error: %s", err)
		return []DockerData{}, err
	}
	for _, network := range networkList {
		networkToAdd := network
		networkMap[network.ID] = &networkToAdd
	}

	var dockerDataList []DockerData

	for _, service := range serviceList {
		dockerData := parseService(service, networkMap)

		dockerDataList = append(dockerDataList, dockerData)
	}
	return dockerDataList, err

}

func parseService(service swarmtypes.Service, networkMap map[string]*dockertypes.NetworkResource) DockerData {
	dockerData := DockerData{
		Name:            service.Spec.Annotations.Name,
		Labels:          service.Spec.Annotations.Labels,
		NetworkSettings: NetworkSettings{},
	}

	if service.Spec.EndpointSpec != nil {
		switch service.Spec.EndpointSpec.Mode {
		case swarm.ResolutionModeDNSRR:
			log.Debug("Ignored endpoint-mode not supported, service name: %s", dockerData.Name)
		case swarm.ResolutionModeVIP:
			dockerData.NetworkSettings.Networks = make(map[string]*NetworkData)
			for _, virtualIP := range service.Endpoint.VirtualIPs {
				networkService := networkMap[virtualIP.NetworkID]
				if networkService != nil {
					ip, _, _ := net.ParseCIDR(virtualIP.Addr)
					network := &NetworkData{
						Name: networkService.Name,
						ID:   virtualIP.NetworkID,
						Addr: ip.String(),
					}
					dockerData.NetworkSettings.Networks[network.Name] = network
				} else {
					log.Debug("Network not found, id: %s", virtualIP.NetworkID)
				}

			}
		}
	}
	return dockerData
}
