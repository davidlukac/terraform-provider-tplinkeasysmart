package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// An update must reach the switch and be reflected in the state: after the
// apply Terraform re-plans and demands an empty plan, so a resource that stores
// something other than what the switch holds fails here.
func TestResourcesApplyAnUpdate(t *testing.T) {
	for _, sc := range faultScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			inner := sc.fake()
			var writesAfterCreate int
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(inner),
				Steps: []resource.TestStep{
					{Config: sc.create, Check: func(*terraformState) error {
						inner.mu.Lock()
						defer inner.mu.Unlock()
						writesAfterCreate = inner.sets
						return nil
					}},
					{Config: sc.update, Check: func(*terraformState) error {
						inner.mu.Lock()
						defer inner.mu.Unlock()
						if inner.sets <= writesAfterCreate {
							return fmt.Errorf("the update did not write to the switch (%d writes after create, %d after update)", writesAfterCreate, inner.sets)
						}
						return nil
					}},
				},
			})
		})
	}
}
