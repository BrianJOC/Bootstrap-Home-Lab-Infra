package stages_test

import (
	"os"
	"strings"
	"testing"
)

// TestConfigStageDoesNotWriteEnv guards against re-introducing the .env write
// inside ConfigStage. The wizard path (main.go) is the sole writer of .env.
func TestConfigStageDoesNotWriteEnv(t *testing.T) {
	src, err := os.ReadFile("stage0_config.go")
	if err != nil {
		t.Skip("cannot read stage0_config.go:", err)
	}
	if strings.Contains(string(src), "writeEnvFile") || strings.Contains(string(src), "WriteEnvFile") {
		t.Error("stage0_config.go must not contain writeEnvFile or WriteEnvFile calls; " +
			".env is written only by the wizard path in main.go")
	}
}
