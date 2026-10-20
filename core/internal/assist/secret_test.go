package assist

// secret_test.go, TanyAI slice 1 round 2 (review P2-1): every type that holds a key prints without
// it, under every verb and as JSON, by value and by pointer. A new holder is added to this table.

import (
	"encoding/json"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
)

const probeKey = "sk-probe-9f8e7d6c5b4a"

func TestSecret_EveryKeyHolderFormatsWithoutTheKey(t *testing.T) {
	cfg := ModelConfig{AnthropicKey: redact.NewSecret(probeKey), GatewayKey: redact.NewSecret(probeKey), GatewayURL: "http://gw", DealerTag: "tag"}
	oaiCfg := OpenAICompatConfig{BaseURL: "http://gw", Key: redact.NewSecret(probeKey), DealerTag: "tag", Model: "m"}
	router := NewRouter(nil, cfg, nil, nil)
	anth := NewAnthropicProvider(probeKey, "m", "http://gw")
	oai := NewOpenAICompatProvider(oaiCfg)
	rcfg := readtx.Config{ReaderURL: redact.NewSecret(probeKey), ProReaderURL: redact.NewSecret(probeKey), ClaimsKey: redact.NewSecret(probeKey)}
	signer, err := readtx.NewSigner(redact.NewSecret(probeKey + "-padded-to-the-minimum-length"))
	if err != nil {
		t.Fatal(err)
	}
	holders := map[string]any{
		"readtx.Config":           rcfg,
		"*readtx.Config":          &rcfg,
		"readtx.Signer":           *signer,
		"*readtx.Signer":          signer,
		"readtx.Readers":          readtx.Readers{},
		"redact.Secret":           redact.NewSecret(probeKey),
		"*redact.Secret":          func() *redact.Secret { s := redact.NewSecret(probeKey); return &s }(),
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
		"map holding a secret":    map[string]redact.Secret{"k": redact.NewSecret(probeKey)},
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
	if redact.NewSecret(probeKey).Reveal() != probeKey {
		t.Fatal("Reveal must return the value")
	}
	if got := redact.NewSecret("").String(); got == redact.NewSecret(probeKey).String() {
		t.Fatal("set and unset must be told apart")
	}
}
