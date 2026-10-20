package assist

// secret_test.go, TanyAI slice 1 round 2 (review P2-1): every type that holds a key prints without
// it, under every verb and as JSON, by value and by pointer. A new holder is added to this table.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const probeKey = "sk-probe-9f8e7d6c5b4a"

func TestSecret_EveryKeyHolderFormatsWithoutTheKey(t *testing.T) {
	cfg := ModelConfig{AnthropicKey: Secret(probeKey), GatewayKey: Secret(probeKey), GatewayURL: "http://gw", DealerTag: "tag"}
	oaiCfg := OpenAICompatConfig{BaseURL: "http://gw", Key: Secret(probeKey), DealerTag: "tag", Model: "m"}
	router := NewRouter(nil, cfg, nil, nil)
	anth := NewAnthropicProvider(probeKey, "m", "http://gw")
	oai := NewOpenAICompatProvider(oaiCfg)
	holders := map[string]any{
		"Secret":                  Secret(probeKey),
		"*Secret":                 func() *Secret { s := Secret(probeKey); return &s }(),
		"ModelConfig":             cfg,
		"*ModelConfig":            &cfg,
		"OpenAICompatConfig":      oaiCfg,
		"*OpenAICompatConfig":     &oaiCfg,
		"Router":                  *router,
		"*Router":                 router,
		"AnthropicProvider":       *anth,
		"*AnthropicProvider":      anth,
		"OpenAICompatProvider":    *oai,
		"*OpenAICompatProvider":   oai,
		"struct holding a config": struct{ C ModelConfig }{cfg},
		"struct holding a router": struct{ R *Router }{router},
		"slice of providers":      []Provider{anth, oai},
		"map holding a secret":    map[string]Secret{"k": Secret(probeKey)},
	}
	for name, v := range holders {
		outs := []string{
			fmt.Sprintf("%v", v), fmt.Sprintf("%+v", v), fmt.Sprintf("%#v", v), fmt.Sprintf("%s", v), fmt.Sprintf("%q", v),
			fmt.Sprint(v), fmt.Sprintf("%x", v),
		}
		if b, err := json.Marshal(v); err == nil {
			outs = append(outs, string(b))
		}
		for _, o := range outs {
			if strings.Contains(o, probeKey) || strings.Contains(o, fmt.Sprintf("%x", probeKey)) {
				t.Errorf("%s prints the key: %q", name, o)
			}
		}
	}
}

func TestSecret_RevealIsTheOnlyWayOut(t *testing.T) {
	if Secret(probeKey).Reveal() != probeKey {
		t.Fatal("Reveal must return the value")
	}
	if got := Secret("").String(); got == Secret(probeKey).String() {
		t.Fatal("set and unset must be told apart")
	}
}
