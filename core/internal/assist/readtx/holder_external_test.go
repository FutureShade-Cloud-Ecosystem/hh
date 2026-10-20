package readtx_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
)

// The other half of the holder check: every credential holder of package assist
// formats without its key, here too, so the two tests together cover every holder
// of the one Secret type from either side of the import.
func TestEveryAssistHolderFormatsWithoutTheKey(t *testing.T) {
	const key = "sk-probe-readtx-1a2b3c4d5e6f"
	cfg := assist.ModelConfig{AnthropicKey: redact.NewSecret(key), GatewayKey: redact.NewSecret(key)}
	oaiCfg := assist.OpenAICompatConfig{BaseURL: "http://gw", Key: redact.NewSecret(key), Model: "m"}
	router := assist.NewRouter(nil, cfg, nil, nil)
	anth := assist.NewAnthropicProvider(key, "m", "http://gw")
	oai := assist.NewOpenAICompatProvider(oaiCfg)
	holders := map[string]any{
		"ModelConfig": cfg, "*ModelConfig": &cfg, "OpenAICompatConfig": oaiCfg, "*OpenAICompatConfig": &oaiCfg,
		"Router": *router, "*Router": router, "AnthropicProvider": *anth, "*AnthropicProvider": anth,
		"OpenAICompatProvider": *oai, "*OpenAICompatProvider": oai,
	}
	for name, v := range holders {
		outs := []string{fmt.Sprintf("%v", v), fmt.Sprintf("%+v", v), fmt.Sprintf("%#v", v), fmt.Sprintf("%s", v), fmt.Sprintf("%q", v), fmt.Sprintf("%x", v)}
		if b, err := json.Marshal(v); err == nil {
			outs = append(outs, string(b))
		}
		for _, o := range outs {
			if strings.Contains(o, key) || strings.Contains(o, fmt.Sprintf("%x", key)) {
				t.Errorf("%s prints the key: %q", name, o)
			}
		}
	}
}
