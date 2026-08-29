package stages

import (
	"fmt"
	"os"
	"strings"

	terraformrunner "github.com/homelab/bootstrap-home-infra/internal/terraform"
)

// terraformHandoffStatus reports whether GitLab is authoritative for a
// component and verifies that the protected local rollback state still exists.
// A completed handoff is a hard ownership boundary: callers must diagnose
// runtime drift rather than allowing the bootstrap runner to re-apply locally.
func terraformHandoffStatus(component string) (bool, error) {
	statePath, err := terraformrunner.StatePath(component)
	if err != nil {
		return false, err
	}
	handoffPath := strings.TrimSuffix(statePath, ".tfstate") + ".handoff.json"
	if _, err = os.Stat(handoffPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("checking %s Terraform handoff: %w", component, err)
	}
	if _, err = os.Stat(statePath); err != nil {
		return false, fmt.Errorf("protected local %s rollback state is missing after GitLab handoff: %w", component, err)
	}
	return true, nil
}
