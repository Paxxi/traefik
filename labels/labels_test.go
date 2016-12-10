package labels

import (
	"reflect"
	"strings"
	"testing"
)

func TestGetLabel(t *testing.T) {
	testData := []struct {
		labels   map[string]string
		expected string
	}{
		{
			labels:   map[string]string{},
			expected: "Label not found:",
		},
		{
			labels: map[string]string{
				"foo": "bar",
			},
			expected: "",
		},
	}

	for _, e := range testData {
		result, err := GetLabel(e.labels, "foo")
		if e.expected != "" {
			if err == nil || !strings.Contains(err.Error(), e.expected) {
				t.Fatalf("expected an error with %q, got %v", e.expected, err)
			}
		} else {
			if result != "bar" {
				t.Fatalf("expected label 'bar', got %s", result)
			}
		}
	}
}

func TestGetLabels(t *testing.T) {
	testData := []struct {
		labels         map[string]string
		expectedLabels map[string]string
		expectedError  string
	}{
		{
			labels:         map[string]string{},
			expectedLabels: map[string]string{},
			expectedError:  "Label not found:",
		},
		{
			labels: map[string]string{
				"foo": "fooz",
			},
			expectedLabels: map[string]string{
				"foo": "fooz",
			},
			expectedError: "Label not found: bar",
		},
		{
			labels: map[string]string{
				"foo": "fooz",
				"bar": "barz",
			},
			expectedLabels: map[string]string{
				"foo": "fooz",
				"bar": "barz",
			},
			expectedError: "",
		},
	}

	for _, e := range testData {
		result, err := GetLabels(e.labels, []string{"foo", "bar"})
		if !reflect.DeepEqual(result, e.expectedLabels) {
			t.Fatalf("expect %v, got %v", e.expectedLabels, result)
		}
		if e.expectedError != "" {
			if err == nil || !strings.Contains(err.Error(), e.expectedError) {
				t.Fatalf("expected an error with %q, got %v", e.expectedError, err)
			}
		}
	}
}

func TestGetFrontendName(t *testing.T) {
	testData := []struct {
		labels   map[string]string
		expected string
	}{
		{
			labels: map[string]string{
				"traefik.frontend.rule": "Headers:User-Agent,bat/0.1.0",
			},
			expected: "Headers-User-Agent-bat-0-1-0",
		},
		{
			labels: map[string]string{
				"traefik.frontend.rule": "Host:foo.bar",
			},
			expected: "Host-foo-bar",
		},
		{
			labels: map[string]string{
				"traefik.frontend.rule": "Path:/test",
			},
			expected: "Path-test",
		},
		{
			labels: map[string]string{
				"traefik.frontend.rule": "PathPrefix:/test2",
			},
			expected: "PathPrefix-test2",
		},
	}

	for _, e := range testData {
		actual := GetFrontendName(e.labels, "err")
		if strings.Compare(actual, e.expected) != 0 {
			t.Fatalf("expected %q got %q", e.expected, actual)
		}
	}
}

func TestGetFrontendRule(t *testing.T) {
	testData := []struct {
		labels   map[string]string
		expected string
	}{
		{
			labels: map[string]string{
				"traefik.frontend.rule": "Host:foo.bar",
			},
			expected: "Host:foo.bar",
		},
		{
			labels: map[string]string{
				"traefik.frontend.rule": "Path:/test",
			},
			expected: "Path:/test",
		},
	}

	for _, e := range testData {
		actual := GetFrontendRule(e.labels, "err")
		if strings.Compare(actual, e.expected) != 0 {
			t.Fatalf("expected %q got %q", e.expected, actual)
		}
	}
}

func TestGetBackend(t *testing.T) {
	testData := []struct {
		labels   map[string]string
		expected string
	}{
		{
			labels: map[string]string{
				"traefik.backend": "foobar",
			},
			expected: "foobar",
		},
		{
			labels: map[string]string{
				"traefik.b": "foobar",
			},
			expected: "default",
		},
	}

	for _, e := range testData {
		actual := GetBackend(e.labels, "default")
		if strings.Compare(actual, e.expected) != 0 {
			t.Fatalf("expected %q got %q", e.expected, actual)
		}
	}
}

func TestGetPort(t *testing.T) {
	testData := []struct {
		labels   map[string]string
		expected string
	}{
		{
			labels: map[string]string{
				"traefik.port": "80",
			},
			expected: "80",
		},
		{
			labels: map[string]string{
				"traefik.p": "foobar",
			},
			expected: "",
		},
	}

	for _, e := range testData {
		actual := GetPort(e.labels)
		if strings.Compare(actual, e.expected) != 0 {
			t.Fatalf("expected %q got %q", e.expected, actual)
		}
	}
}

func TestGetWeight(t *testing.T) {
	testData := []struct {
		labels   map[string]string
		expected string
	}{
		{
			labels: map[string]string{
				"traefik.weight": "80",
			},
			expected: "80",
		},
		{
			labels:   map[string]string{},
			expected: "0",
		},
	}

	for _, e := range testData {
		actual := GetWeight(e.labels)
		if strings.Compare(actual, e.expected) != 0 {
			t.Fatalf("expected %q got %q", e.expected, actual)
		}
	}
}

func TestGetDomain(t *testing.T) {
	testData := []struct {
		labels   map[string]string
		expected string
	}{
		{
			labels: map[string]string{
				"traefik.domain": "foo.bar",
			},
			expected: "foo.bar",
		},
		{
			labels:   map[string]string{},
			expected: "default",
		},
	}

	for _, e := range testData {
		actual := GetDomain(e.labels, "default")
		if strings.Compare(actual, e.expected) != 0 {
			t.Fatalf("expected %q got %q", e.expected, actual)
		}
	}
}

func TestGetProtocol(t *testing.T) {
	testData := []struct {
		labels   map[string]string
		expected string
	}{
		{
			labels: map[string]string{
				"traefik.protocol": "http",
			},
			expected: "http",
		},
	}

	for _, e := range testData {
		actual := GetProtocol(e.labels)
		if strings.Compare(actual, e.expected) != 0 {
			t.Fatalf("expected %q got %q", e.expected, actual)
		}
	}
}

func TestGetPassHostHeader(t *testing.T) {
	testData := []struct {
		labels   map[string]string
		expected string
	}{
		{
			labels: map[string]string{
				"traefik.frontend.passHostHeader": "false",
			},
			expected: "false",
		},
		{
			labels:   map[string]string{},
			expected: "true",
		},
	}

	for _, e := range testData {
		actual := GetPassHostHeader(e.labels)
		if strings.Compare(actual, e.expected) != 0 {
			t.Fatalf("expected %q got %q", e.expected, actual)
		}
	}
}
