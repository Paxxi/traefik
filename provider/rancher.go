package provider

import (
	"errors"
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
	"github.com/rancher/go-rancher-metadata/metadata"
)

type RancherMetadata struct {
	BaseProvider `mapstructure:",squash"`
	Endpoint     string `description:"Rancher metadata endpoint http://rancher-metadata/2015-12-19"`
	Client       metadata.Client
}

func (provider *RancherMetadata) hasCircuitBreakerLabel(container metadata.Service) bool {
	return labels.HasCircuitBreakerLabel(container.Labels)
}

func (provider *RancherMetadata) hasLoadBalancerLabel(container metadata.Service) bool {
	return labels.HasLoadBalancerLabel(container.Labels)
}

func (provider *RancherMetadata) hasMaxConnLabels(container metadata.Service) bool {
	return labels.HasMaxConnLabels(container.Labels)
}

func (provider *RancherMetadata) getCircuitBreakerExpression(container metadata.Service) string {
	return labels.GetCircuitBreakerExpression(container.Labels)
}

func (provider *RancherMetadata) getLoadBalancerMethod(container metadata.Service) string {
	return labels.GetLoadBalancerMethod(container.Labels)
}

func (provider *RancherMetadata) getMaxConnAmount(container metadata.Service) int64 {
	return labels.GetMaxConnAmount(container.Labels)
}

func (provider *RancherMetadata) getMaxConnExtractorFunc(container metadata.Service) string {
	return labels.GetMaxConnExtractorFunc(container.Labels)
}
func (provider *RancherMetadata) getFrontendName(container metadata.Service) string {
	return labels.GetFrontendName(container.Labels, "Host:"+container.Fqdn)
}

// GetFrontendRule returns the frontend rule for the specified container, using
// it's label. It returns a default one (Host) if the label is not present.
func (provider *RancherMetadata) getFrontendRule(container metadata.Service) string {
	return labels.GetFrontendName(container.Labels, "Host:"+container.Fqdn)
}

func (provider *RancherMetadata) getBackend(container metadata.Service) string {
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

func (provider *RancherMetadata) getWeight(container metadata.Service) string {
	return labels.GetWeight(container.Labels)
}

func (provider *RancherMetadata) getSticky(container metadata.Service) string {
	return labels.GetSticky(container.Labels)
}

func (provider *RancherMetadata) getDomain(container metadata.Service) string {
	return labels.GetDomain(container.Labels, container.Fqdn)
}

func (provider *RancherMetadata) getProtocol(container metadata.Service) string {
	return labels.GetProtocol(container.Labels)
}

func (provider *RancherMetadata) getPassHostHeader(container metadata.Service) string {
	return labels.GetPassHostHeader(container.Labels)
}

func (provider *RancherMetadata) getPriority(container metadata.Service) string {
	return labels.GetPriority(container.Labels)
}

func (provider *RancherMetadata) getEntryPoints(container metadata.Service) []string {
	return labels.GetEntryPoints(container.Labels)
}

func (provider *RancherMetadata) isContainerEnabled(container metadata.Service, exposedByDefault bool) bool {
	return labels.IsContainerEnabled(container.Labels, exposedByDefault)
}

func (provider *RancherMetadata) buildConfig(services []metadata.Service) *types.Configuration {
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

	allNodes := []metadata.Container{}
	for _, s := range services {
		allNodes = append(allNodes, s.Containers...)
	}
	// Ensure a stable ordering of nodes so that identical configurations may be detected
	// sort.Sort(nodeSorter(allNodes))

	templateObjects := struct {
		Services []metadata.Service
		Nodes    []metadata.Container
	}{
		Services: services,
		Nodes:    allNodes,
	}

	configuration, err := provider.getConfiguration("templates/rancher_metadata.tmpl", FuncMap, templateObjects)
	if err != nil {
		log.WithError(err).Error("Failed to create config")
	}

	return configuration
}

func (provider *RancherMetadata) filterContainers(service *metadata.Service) bool {
	if len(service.Containers) == 0 {
		return false
	}

	containers := fun.Filter(func(container metadata.Container) bool {
		for k := range container.Labels {
			if strings.Contains(k, "traefik") {
				return strings.Contains(strings.ToLower(container.HealthState), "healthy")
			}
		}
		return false
	}, service.Containers).([]metadata.Container)

	if len(containers) == 0 {
		return false
	}

	service.Containers = containers

	return true
}

func (provider *RancherMetadata) getServices() ([]metadata.Service, error) {
	services, err := provider.Client.GetServices()
	if err != nil {
		return nil, err
	}

	validServices := fun.Filter(func(service metadata.Service) bool {
		for k := range service.Labels {
			if strings.Contains(k, "traefik") && provider.filterContainers(&service) {
				return true
			}
		}
		return false
	}, services).([]metadata.Service)

	return validServices, nil
}

func (provider *RancherMetadata) pollServices(stopCh <-chan struct{}) <-chan []metadata.Service {
	watchCh := make(chan []metadata.Service)

	tickerCh := time.NewTicker(5 * time.Second).C

	safe.Go(func() {
		defer close(watchCh)

		for {
			select {
			case <-stopCh:
				return
			case <-tickerCh:
				data, err := provider.getServices()
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
