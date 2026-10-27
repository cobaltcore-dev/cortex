// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package failoverhost

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gophercloud/gophercloud/v2"
)

// flavorClientAgainst builds a novaFlavorClient whose Nova service client points
// at the given test server, bypassing Connect (which is just auth/endpoint
// wiring over already-tested packages). The endpoint must end with "/" for
// gophercloud's ServiceURL to build "flavors/detail" correctly.
func flavorClientAgainst(url string) *novaFlavorClient {
	return &novaFlavorClient{sc: &gophercloud.ServiceClient{
		ProviderClient: &gophercloud.ProviderClient{},
		Endpoint:       url + "/",
	}}
}

// TestListFlavorsMapsFields checks ListFlavors maps gophercloud's flavor shape
// onto our trimmed Flavor: name, RAM (reported in MiB) -> MemoryMB, vcpus, and
// the embedded extra_specs (available at microversion 2.61, which Connect sets).
func TestListFlavorsMapsFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"flavors":[
			{"name":"m1.large","ram":8192,"vcpus":4,"extra_specs":{"capabilities:hypervisor_type":"QEMU"}},
			{"name":"m1.small","ram":2048,"vcpus":1,"extra_specs":{}}
		]}`))
	}))
	defer server.Close()

	got, err := flavorClientAgainst(server.URL).ListFlavors(context.Background())
	if err != nil {
		t.Fatalf("ListFlavors: %v", err)
	}
	want := []Flavor{
		{Name: "m1.large", MemoryMB: 8192, VCPUs: 4, ExtraSpecs: map[string]string{"capabilities:hypervisor_type": "QEMU"}},
		{Name: "m1.small", MemoryMB: 2048, VCPUs: 1, ExtraSpecs: map[string]string{}},
	}
	if len(got) != len(want) {
		t.Fatalf("flavors = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Name != want[i].Name || got[i].MemoryMB != want[i].MemoryMB || got[i].VCPUs != want[i].VCPUs {
			t.Errorf("flavor[%d] = %+v, want %+v", i, got[i], want[i])
		}
		if !maps.Equal(got[i].ExtraSpecs, want[i].ExtraSpecs) {
			t.Errorf("flavor[%d] extra specs = %v, want %v", i, got[i].ExtraSpecs, want[i].ExtraSpecs)
		}
	}
}

// TestListFlavorsFollowsPagination checks ListFlavors walks every page via the
// flavors_links "next" reference, so a flavor on a later page is not dropped.
func TestListFlavorsFollowsPagination(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.RawQuery, "marker=page2") {
			_, _ = w.Write([]byte(`{"flavors":[{"name":"second","ram":1024,"vcpus":1}]}`))
			return
		}
		// First page points at the second via a next link.
		_, _ = w.Write([]byte(`{"flavors":[{"name":"first","ram":1024,"vcpus":1}],
			"flavors_links":[{"rel":"next","href":"` + server.URL + `/flavors/detail?marker=page2"}]}`))
	}))
	defer server.Close()

	got, err := flavorClientAgainst(server.URL).ListFlavors(context.Background())
	if err != nil {
		t.Fatalf("ListFlavors: %v", err)
	}
	names := make([]string, len(got))
	for i, f := range got {
		names[i] = f.Name
	}
	slices.Sort(names)
	if want := []string{"first", "second"}; !slices.Equal(names, want) {
		t.Errorf("flavor names = %v, want %v", names, want)
	}
}

// TestListFlavorsErrorsOnNon200 checks a Nova error surfaces as an error rather
// than an empty list, so the seeder aborts the pass instead of shrinking the
// seed set.
func TestListFlavorsErrorsOnNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	if _, err := flavorClientAgainst(server.URL).ListFlavors(context.Background()); err == nil {
		t.Fatal("ListFlavors: expected error on non-200, got nil")
	}
}
