package labels

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/containous/traefik/log"
)

func normalize(name string) string {
	fargs := func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.IsNumber(c)
	}
	// get function
	return strings.Join(strings.FieldsFunc(name, fargs), "-")
}
func GetLabel(labels map[string]string, label string) (string, error) {
	for key, value := range labels {
		if key == label {
			return value, nil
		}
	}
	return "", errors.New("Label not found:" + label)
}
func GetLabels(containerLabels map[string]string, labels []string) (map[string]string, error) {
	var globalErr error
	foundLabels := map[string]string{}
	for _, label := range labels {
		foundLabel, err := GetLabel(containerLabels, label)
		// Error out only if one of them is defined.
		if err != nil {
			globalErr = errors.New("Label not found: " + label)
			continue
		}
		foundLabels[label] = foundLabel

	}
	return foundLabels, globalErr
}

func GetDomain(labels map[string]string, defaultValue string) string {
	if label, err := GetLabel(labels, "traefik.domain"); err == nil {
		return label
	}
	return defaultValue
}

func GetProtocol(labels map[string]string) string {
	if label, err := GetLabel(labels, "traefik.protocol"); err == nil {
		return label
	}
	return "http"
}

func GetPassHostHeader(labels map[string]string) string {
	if passHostHeader, err := GetLabel(labels, "traefik.frontend.passHostHeader"); err == nil {
		return passHostHeader
	}
	return "true"
}

func GetPriority(labels map[string]string) string {
	if priority, err := GetLabel(labels, "traefik.frontend.priority"); err == nil {
		return priority
	}
	return "0"
}

func GetEntryPoints(labels map[string]string) []string {
	if entryPoints, err := GetLabel(labels, "traefik.frontend.entryPoints"); err == nil {
		return strings.Split(entryPoints, ",")
	}
	return []string{}
}

func IsContainerEnabled(labels map[string]string, exposedByDefault bool) bool {
	return exposedByDefault && labels["traefik.enable"] != "false" || labels["traefik.enable"] == "true"
}

func GetPort(labels map[string]string) string {
	if label, err := GetLabel(labels, "traefik.port"); err == nil {
		return label
	}

	return ""
}

func GetWeight(labels map[string]string) string {
	if label, err := GetLabel(labels, "traefik.weight"); err == nil {
		return label
	}
	return "0"
}

func GetSticky(labels map[string]string) string {
	if _, err := GetLabel(labels, "traefik.backend.loadbalancer.sticky"); err == nil {
		return "true"
	}
	return "false"
}

func GetFrontendName(labels map[string]string, defaultValue string) string {
	// Replace '.' with '-' in quoted keys because of this issue https://github.com/BurntSushi/toml/issues/78
	return normalize(GetFrontendRule(labels, defaultValue))
}

// GetFrontendRule returns the frontend rule for the specified container, using
// it's label. It returns a default one (Host) if the label is not present.
func GetFrontendRule(labels map[string]string, defaultValue string) string {
	if label, err := GetLabel(labels, "traefik.frontend.rule"); err == nil {
		return label
	}

	return defaultValue
}

func GetBackend(labels map[string]string, defaultValue string) string {
	if label, err := GetLabel(labels, "traefik.backend"); err == nil {
		return normalize(label)
	}
	return normalize(defaultValue)
}

func HasCircuitBreakerLabel(labels map[string]string) bool {
	if _, err := GetLabel(labels, "traefik.backend.circuitbreaker.expression"); err != nil {
		return false
	}
	return true
}

func HasLoadBalancerLabel(labels map[string]string) bool {
	_, errMethod := GetLabel(labels, "traefik.backend.loadbalancer.method")
	_, errSticky := GetLabel(labels, "traefik.backend.loadbalancer.sticky")
	if errMethod != nil && errSticky != nil {
		return false
	}
	return true
}

func HasMaxConnLabels(labels map[string]string) bool {
	if _, err := GetLabel(labels, "traefik.backend.maxconn.amount"); err != nil {
		return false
	}
	if _, err := GetLabel(labels, "traefik.backend.maxconn.extractorfunc"); err != nil {
		return false
	}
	return true
}

func GetCircuitBreakerExpression(labels map[string]string) string {
	if label, err := GetLabel(labels, "traefik.backend.circuitbreaker.expression"); err == nil {
		return label
	}
	return "NetworkErrorRatio() > 1"
}

func GetLoadBalancerMethod(labels map[string]string) string {
	if label, err := GetLabel(labels, "traefik.backend.loadbalancer.method"); err == nil {
		return label
	}
	return "wrr"
}

func GetMaxConnAmount(labels map[string]string) int64 {
	if label, err := GetLabel(labels, "traefik.backend.maxconn.amount"); err == nil {
		i, errConv := strconv.ParseInt(label, 10, 64)
		if errConv != nil {
			log.Errorf("Unable to parse traefik.backend.maxconn.amount %s", label)
			return math.MaxInt64
		}
		return i
	}
	return math.MaxInt64
}

func GetMaxConnExtractorFunc(labels map[string]string) string {
	if label, err := GetLabel(labels, "traefik.backend.maxconn.extractorfunc"); err == nil {
		return label
	}
	return "request.host"
}
