package processing

import (
	"errors"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// LoadConfig shares the deployment contract with server and evaluation assembly.
// Reading configuration performs no network calls and never exposes credentials.
func LoadConfig() (Config, error) {
	if _, present := os.LookupEnv("MEMSTATION_MEMORY_PROCESSING_ENTITY_RECALL_ENABLED"); present {
		return Config{}, errors.New("MEMSTATION_MEMORY_PROCESSING_ENTITY_RECALL_ENABLED is unsupported; entity-assisted retrieval is always active")
	}
	v := viper.New()
	for _, key := range []string{"strategy", "endpoint", "token", "model", "revision", "splitter", "threshold", "audit_rate", "max_semantic_bytes", "entity_recall_timeout_ms"} {
		if err := v.BindEnv(key, "MEMSTATION_MEMORY_PROCESSING_"+strings.ToUpper(key)); err != nil {
			return Config{}, err
		}
	}
	v.SetDefault("strategy", "generative")
	v.SetDefault("entity_recall_timeout_ms", 200)
	v.SetDefault("splitter", "char")
	v.SetDefault("threshold", .75)
	v.SetDefault("audit_rate", .1)
	v.SetDefault("max_semantic_bytes", 128*1024)
	c := Config{Strategy: v.GetString("strategy"), EntityRecallTimeoutMS: v.GetInt("entity_recall_timeout_ms"), Endpoint: v.GetString("endpoint"), Token: v.GetString("token"), Model: v.GetString("model"), Revision: v.GetString("revision"), Splitter: v.GetString("splitter"), Threshold: v.GetFloat64("threshold"), AuditRate: v.GetFloat64("audit_rate"), MaxSemanticBytes: v.GetInt("max_semantic_bytes")}
	return c, c.Validate()
}
