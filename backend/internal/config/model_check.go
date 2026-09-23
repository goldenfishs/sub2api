package config

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/modelcheckbridge"
)

// ModelCheckConfig enables the separately packaged local companion. Keeping it
// opt-in prevents a frontend build from exposing an undeployed service.
type ModelCheckConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	Upstream     string `mapstructure:"upstream"`
	BridgeSecret string `mapstructure:"bridge_secret" json:"-" yaml:"-"`
}

func (c ModelCheckConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if !modelcheckbridge.ValidSecret(c.BridgeSecret) {
		return fmt.Errorf("model_check.bridge_secret must contain 32 to 256 printable ASCII characters without spaces")
	}
	if _, err := modelcheckbridge.ParseLoopbackOrigin(c.Upstream); err != nil {
		return fmt.Errorf("model_check.upstream: %w", err)
	}
	return nil
}
