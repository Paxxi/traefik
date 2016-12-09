package provider

import (
	"errors"
	"strconv"
	"text/template"
	"time"

	"strings"

	"github.com/BurntSushi/ty/fun"
	"github.com/cenk/backoff"
	"github.com/containous/traefik/job"
	"github.com/containous/traefik/labels"
	"github.com/containous/traefik/log"
	"github.com/containous/traefik/safe"
	"github.com/containous/traefik/types"
	"github.com/docker/go-connections/nat"
	"github.com/rancher/go-rancher-metadata/metadata"
)

type RancherMetadata struct {
	BaseProvider     `mapstructure:",squash"`
	Endpoint         string `description:"Rancher metadata endpoint http://rancher-metadata/2015-12-19"`
	Client           metadata.Client
	ExposedByDefault bool `description:"Should ports be exposed by default"`
}

func (provider *RancherMetadata) hasCircuitBreakerLabel(container DockerData) bool {
	return labels.HasCircuitBreakerLabel(container.Labels)
}

func (provider *RancherMetadata) hasLoadBalancerLabel(container DockerData) bool {
	return labels.HasLoadBalancerLabel(container.Labels)
}

func (provider *RancherMetadata) hasMaxConnLabels(container DockerData) bool {
	return labels.HasMaxConnLabels(container.Labels)
}

func (provider *RancherMetadata) getCircuitBreakerExpression(container DockerData) string {
	return labels.GetCircuitBreakerExpression(container.Labels)
}

func (provider *RancherMetadata) getLoadBalancerMethod(container DockerData) string {
	return labels.GetLoadBalancerMethod(container.Labels)
}

func (provider *RancherMetadata) getMaxConnAmount(container DockerData) int64 {
	return labels.GetMaxConnAmount(container.Labels)
}

func (provider *RancherMetadata) getMaxConnExtractorFunc(container DockerData) string {
	return labels.GetMaxConnExtractorFunc(container.Labels)
}
func (provider *RancherMetadata) getFrontendName(container DockerData) string {
	return labels.GetFrontendName(container.Labels, "Host:Error")
}

// GetFrontendRule returns the frontend rule for the specified container, using
// it's label. It returns a default one (Host) if the label is not present.
func (provider *RancherMetadata) getFrontendRule(container DockerData) string {
	return labels.GetFrontendName(container.Labels, "Host:Error")
}

func (provider *RancherMetadata) getBackend(container DockerData) string {
	return labels.GetBackend(container.Labels, container.Name)
}

func (provider *RancherMetadata) getIPAddress(container metadata.Container) string {
	label, err := labels.GetLabel(container.Labels, "traefik.RancherMetadata.network")
	if err == nil && label != "" {
		return container.PrimaryIp
	}

	return ""
}

func (provider *RancherMetadata) getPort(container metadata.Container) string {
	if label := labels.GetPort(container.Labels); len(label) > 0 {
		return label
	}

	for _, v := range container.Ports {
		return v
	}
	return ""
}

func (provider *RancherMetadata) getWeight(container DockerData) string {
	return labels.GetWeight(container.Labels)
}

func (provider *RancherMetadata) getSticky(container DockerData) string {
	return labels.GetSticky(container.Labels)
}

func (provider *RancherMetadata) getDomain(container DockerData) string {
	return labels.GetDomain(container.Labels, "Error")
}

func (provider *RancherMetadata) getProtocol(container DockerData) string {
	return labels.GetProtocol(container.Labels)
}

func (provider *RancherMetadata) getPassHostHeader(container DockerData) string {
	return labels.GetPassHostHeader(container.Labels)
}

func (provider *RancherMetadata) getPriority(container DockerData) string {
	return labels.GetPriority(container.Labels)
}

func (provider *RancherMetadata) getEntryPoints(container DockerData) []string {
	return labels.GetEntryPoints(container.Labels)
}

func (provider *RancherMetadata) isContainerEnabled(container metadata.Service, exposedByDefault bool) bool {
	return labels.IsContainerEnabled(container.Labels, exposedByDefault)
}

func (provider *RancherMetadata) buildConfig(containers []DockerData) *types.Configuration {
	var FuncMap = template.FuncMap{
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
	}, containers).([]DockerData)

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
	}{
		filteredContainers,
		frontends,
		backends,
		servers,
	}

	configuration, err := provider.getConfiguration("templates/docker.tmpl", FuncMap, templateObjects)
	if err != nil {
		log.Error(err)
	}
	return configuration
}

func (provider *RancherMetadata) parseContainer(container metadata.Container) DockerData {
	dockerData := DockerData{
		NetworkSettings: NetworkSettings{},
	}

	dockerData.Name = container.Name
	dockerData.Health = container.HealthState
	dockerData.Labels = container.Labels
	if container.Ports != nil {
		for _, p := range container.Ports {
			dockerData.NetworkSettings.Ports[nat.Port(p)] =
				[]nat.PortBinding{
					{
						HostIP:   container.PrimaryIp,
						HostPort: p,
					},
				}
		}
	}
	dockerData.NetworkSettings.Networks[container.NetworkUUID] = &NetworkData{
		ID:   container.NetworkUUID,
		Name: container.NetworkUUID,
		Addr: container.PrimaryIp,
	}

	return dockerData
}

func (provider *RancherMetadata) getContainers() ([]DockerData, error) {
	containers, err := provider.Client.GetContainers()
	if err != nil {
		return nil, err
	}

	result := []DockerData{}
	for _, c := range containers {
		dockerData := provider.parseContainer(c)
		result = append(result, dockerData)
	}

	return result, nil
}

func (provider *RancherMetadata) pollServices(stopCh <-chan struct{}) <-chan []DockerData {
	watchCh := make(chan []DockerData)

	tickerCh := time.NewTicker(5 * time.Second).C

	safe.Go(func() {
		defer close(watchCh)

		for {
			select {
			case <-stopCh:
				return
			case <-tickerCh:
				data, err := provider.getContainers()
				if err != nil {
					log.WithError(err).Errorf("Failed to list services")
					return
				}

				if data != nil {
					watchCh <- data
				}
			default:
			}
		}
	})

	return watchCh
}
func (provider *RancherMetadata) watch(configurationChan chan<- types.ConfigMessage, stop chan bool) error {
	stopCh := make(chan struct{})
	servicesCh := provider.pollServices(stopCh)

	defer close(stopCh)

	for {
		select {
		case <-stop:
			return nil
		case services, err := <-servicesCh:
			if err {
				return errors.New("Rancher service list is empty")
			}

			log.Debug("List of services changed")
			configuration := provider.buildConfig(services)
			configurationChan <- types.ConfigMessage{
				ProviderName:  "rancher_metadata",
				Configuration: configuration,
			}
		}
	}
}
func (provider *RancherMetadata) Provide(configurationChan chan<- types.ConfigMessage, pool *safe.Pool, constraints types.Constraints) error {
	url := provider.Endpoint
	if len(url) == 0 {
		url = "http://rancher-metadata/2015-12-19"
	}
	client, err := metadata.NewClientAndWait(url)
	if err != nil {
		return err
	}

	provider.Client = client
	provider.Constraints = append(provider.Constraints, constraints...)

	pool.Go(func(stop chan bool) {
		notify := func(err error, time time.Duration) {
			log.Errorf("Rancher connection error %+v, retrying in %s", err, time)
		}
		operation := func() error {
			return provider.watch(configurationChan, stop)
		}
		err := backoff.RetryNotify(operation, job.NewBackOff(backoff.NewExponentialBackOff()), notify)
		if err != nil {
			log.Errorf("Cannot connect to rancher server %+v", err)
		}
	})

	return err
}

func (provider *RancherMetadata) containerFilter(container DockerData) bool {
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

func (provider *RancherMetadata) loadDockerConfig(containersInspected []DockerData) *types.Configuration {
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
	}{
		filteredContainers,
		frontends,
		backends,
		servers,
	}

	configuration, err := provider.getConfiguration("templates/rancher.tmpl", DockerFuncMap, templateObjects)
	if err != nil {
		log.Error(err)
	}
	return configuration
}
