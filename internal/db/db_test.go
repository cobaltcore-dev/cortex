package db

import (
	"testing"

	"github.com/cobaltcore-dev/cortex/testlib"
)

func TestMain(m *testing.M) {
	testlib.WithMockDB(m, 5)
}

func TestGet(t *testing.T) {
	db := Get()
	if db == nil {
		t.Errorf("expected db to be initialized")
	}
}
