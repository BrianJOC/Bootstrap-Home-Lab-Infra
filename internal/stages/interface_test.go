package stages

import "github.com/homelab/bootstrap-home-infra/internal/stage"

var (
	_ stage.Stage = (*ConfigStage)(nil)
	_ stage.Stage = (*StorageSetupStage)(nil)
	_ stage.Stage = (*DNSStage)(nil)
	_ stage.Stage = (*TLSStage)(nil)
	_ stage.Stage = (*VaultStage)(nil)
	_ stage.Stage = (*PostgreSQLStage)(nil)
	_ stage.Stage = (*AuthentikStage)(nil)
	_ stage.Stage = (*GitLabStage)(nil)
	_ stage.Stage = (*MAASStage)(nil)
	_ stage.Stage = (*K3sStage)(nil)
	_ stage.Stage = (*ControllerRecoveryStage)(nil)
	_ stage.Stage = (*LifecycleStage)(nil)
)
