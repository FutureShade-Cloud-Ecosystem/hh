//go:build demo

package main

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
)

// The seed repeats the account's and the jobs' pinned ids (the flow table is demo tagged, the seed is
// in both builds); this holds the repeats to the table, in its order.
func TestContractorWorld_PinnedIDsAreTheFlowTables(t *testing.T) {
	if contractorCustomerID != flows.TenantAccountID {
		t.Errorf("the seed's contractor account is %s, the flow table's is %s", contractorCustomerID, flows.TenantAccountID)
	}
	jobs := flows.TenantJobs()
	if len(jobs) != len(contractorJobIDs) {
		t.Fatalf("the flow table holds %d jobs, the seed %d", len(jobs), len(contractorJobIDs))
	}
	for i, j := range jobs {
		if contractorJobIDs[i] != j.ID || contractorJobAddress[i] != j.Address {
			t.Errorf("job %d: seed %s %q, flow table %s %q", i, contractorJobIDs[i], contractorJobAddress[i], j.ID, j.Address)
		}
	}
}
