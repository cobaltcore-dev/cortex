package conf

import (
	"os"
	"testing"
)

func TestForceGetenv(t *testing.T) {
	key := "TEST_FORCE_GETENV"
	value := "test_value"
	os.Setenv(key, value)
	defer os.Unsetenv(key)

	result := forceGetenv(key)
	if result != value {
		t.Errorf("Expected value to be %s, got %s", value, result)
	}
}

func TestForceGetenvEmpty(t *testing.T) {
	key := "TEST_FORCE_GETENV_EMPTY"
	os.Unsetenv(key)

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("The code did not panic")
		}
	}()
	forceGetenv(key)
}

func TestGetenv(t *testing.T) {
	key := "TEST_GETENV"
	value := "test_value"
	defaultValue := "default_value"
	os.Setenv(key, value)
	defer os.Unsetenv(key)

	result := getenv(key, defaultValue)
	if result != value {
		t.Errorf("Expected value to be %s, got %s", value, result)
	}
}

func TestGetenvDefault(t *testing.T) {
	key := "TEST_GETENV_DEFAULT"
	defaultValue := "default_value"
	os.Unsetenv(key)

	result := getenv(key, defaultValue)
	if result != defaultValue {
		t.Errorf("Expected value to be %s, got %s", defaultValue, result)
	}
}

func TestLoadFromEnv(t *testing.T) {
	os.Setenv("OS_AUTH_URL", "http://auth.url")
	os.Setenv("OS_USERNAME", "username")
	os.Setenv("OS_PASSWORD", "password")
	os.Setenv("OS_PROJECT_NAME", "project_name")
	os.Setenv("OS_USER_DOMAIN_NAME", "user_domain_name")
	os.Setenv("OS_PROJECT_DOMAIN_NAME", "project_domain_name")
	os.Setenv("PROMETHEUS_URL", "http://prometheus.url")
	os.Setenv("POSTGRES_HOST", "localhost")
	os.Setenv("POSTGRES_PORT", "5432")
	os.Setenv("POSTGRES_USER", "postgres")
	os.Setenv("POSTGRES_PASSWORD", "secret")

	loadFromEnv()

	if config.OSAuthURL != "http://auth.url" {
		t.Errorf("Expected OSAuthURL to be %s, got %s", "http://auth.url", config.OSAuthURL)
	}
	if config.OSUsername != "username" {
		t.Errorf("Expected OSUsername to be %s, got %s", "username", config.OSUsername)
	}
	if config.OSPassword != "password" {
		t.Errorf("Expected OSPassword to be %s, got %s", "password", config.OSPassword)
	}
	if config.OSProjectName != "project_name" {
		t.Errorf("Expected OSProjectName to be %s, got %s", "project_name", config.OSProjectName)
	}
	if config.OSUserDomainName != "user_domain_name" {
		t.Errorf("Expected OSUserDomainName to be %s, got %s", "user_domain_name", config.OSUserDomainName)
	}
	if config.OSProjectDomainName != "project_domain_name" {
		t.Errorf("Expected OSProjectDomainName to be %s, got %s", "project_domain_name", config.OSProjectDomainName)
	}
	if config.PrometheusURL != "http://prometheus.url" {
		t.Errorf("Expected PrometheusURL to be %s, got %s", "http://prometheus.url", config.PrometheusURL)
	}
	if config.DBHost != "localhost" {
		t.Errorf("Expected DBHost to be %s, got %s", "localhost", config.DBHost)
	}
	if config.DBPort != "5432" {
		t.Errorf("Expected DBPort to be %s, got %s", "5432", config.DBPort)
	}
	if config.DBUser != "postgres" {
		t.Errorf("Expected DBUser to be %s, got %s", "postgres", config.DBUser)
	}
	if config.DBPass != "secret" {
		t.Errorf("Expected DBPass to be %s, got %s", "secret", config.DBPass)
	}
}

func TestGet(t *testing.T) {
	os.Setenv("OS_AUTH_URL", "http://auth.url")
	os.Setenv("OS_USERNAME", "username")
	os.Setenv("OS_PASSWORD", "password")
	os.Setenv("OS_PROJECT_NAME", "project_name")
	os.Setenv("OS_USER_DOMAIN_NAME", "user_domain_name")
	os.Setenv("OS_PROJECT_DOMAIN_NAME", "project_domain_name")
	os.Setenv("PROMETHEUS_URL", "http://prometheus.url")
	os.Setenv("POSTGRES_HOST", "localhost")
	os.Setenv("POSTGRES_PORT", "5432")
	os.Setenv("POSTGRES_USER", "postgres")
	os.Setenv("POSTGRES_PASSWORD", "secret")

	config = nil // Reset the config to ensure it gets loaded again
	result := Get()

	if result.OSAuthURL != "http://auth.url" {
		t.Errorf("Expected OSAuthURL to be %s, got %s", "http://auth.url", result.OSAuthURL)
	}
	if result.OSUsername != "username" {
		t.Errorf("Expected OSUsername to be %s, got %s", "username", result.OSUsername)
	}
	if result.OSPassword != "password" {
		t.Errorf("Expected OSPassword to be %s, got %s", "password", result.OSPassword)
	}
	if result.OSProjectName != "project_name" {
		t.Errorf("Expected OSProjectName to be %s, got %s", "project_name", result.OSProjectName)
	}
	if result.OSUserDomainName != "user_domain_name" {
		t.Errorf("Expected OSUserDomainName to be %s, got %s", "user_domain_name", result.OSUserDomainName)
	}
	if result.OSProjectDomainName != "project_domain_name" {
		t.Errorf("Expected OSProjectDomainName to be %s, got %s", "project_domain_name", result.OSProjectDomainName)
	}
	if result.PrometheusURL != "http://prometheus.url" {
		t.Errorf("Expected PrometheusURL to be %s, got %s", "http://prometheus.url", result.PrometheusURL)
	}
	if result.DBHost != "localhost" {
		t.Errorf("Expected DBHost to be %s, got %s", "localhost", result.DBHost)
	}
	if result.DBPort != "5432" {
		t.Errorf("Expected DBPort to be %s, got %s", "5432", result.DBPort)
	}
	if result.DBUser != "postgres" {
		t.Errorf("Expected DBUser to be %s, got %s", "postgres", result.DBUser)
	}
	if result.DBPass != "secret" {
		t.Errorf("Expected DBPass to be %s, got %s", "secret", result.DBPass)
	}
}
