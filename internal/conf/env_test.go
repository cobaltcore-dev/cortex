// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package conf

import (
	"os"
	"testing"
)

func TestForceGetenv(t *testing.T) {
	key := "TEST_FORCE_GETENV"
	value := "test_value"
	t.Setenv(key, value)
	defer os.Unsetenv(key)

	result := ForceGetenv(key)
	if result != value {
		t.Errorf("expected value to be %s, got %s", value, result)
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
	ForceGetenv(key)
}

func TestGetenv(t *testing.T) {
	key := "TEST_GETENV"
	value := "test_value"
	defaultValue := "default_value"
	t.Setenv(key, value)
	defer os.Unsetenv(key)

	result := Getenv(key, defaultValue)
	if result != value {
		t.Errorf("expected value to be %s, got %s", value, result)
	}
}

func TestGetenvDefault(t *testing.T) {
	key := "TEST_GETENV_DEFAULT"
	defaultValue := "default_value"
	os.Unsetenv(key)

	result := Getenv(key, defaultValue)
	if result != defaultValue {
		t.Errorf("expected value to be %s, got %s", defaultValue, result)
	}
}
