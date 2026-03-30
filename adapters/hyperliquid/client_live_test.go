package hyperliquid

import (
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

func TestSignL1ActionMatchesSDKVector(t *testing.T) {
	priv, err := crypto.HexToECDSA("e908f86dbb4d55ac876378565aafeabc187f6690f046459397b17d9b9a19688e")
	if err != nil {
		t.Fatalf("private key: %v", err)
	}
	action := hlOrderAction{
		Type:     "order",
		Grouping: "na",
		Orders: []hlOrder{{
			Asset:      1,
			IsBuy:      true,
			LimitPx:    "2000.0",
			Size:       "3.5",
			ReduceOnly: false,
			OrderType: hlOrderType{
				Limit: &hlLimit{TIF: "Ioc"},
			},
		}},
	}
	hash, err := hashAction(action, 1583838, nil)
	if err != nil {
		t.Fatalf("hashAction: %v", err)
	}
	sig, err := signL1Action(priv, hash, true)
	if err != nil {
		t.Fatalf("signL1Action: %v", err)
	}
	got := sig.R[2:] + sig.S[2:] + "1c"
	want := "77957e58e70f43b6b68581f2dc42011fc384538a2e5b7bf42d5b936f19fbb67360721a8598727230f67080efee48c812a6a4442013fd3b0eed509171bef9f23f1c"
	if got != want {
		t.Fatalf("mainnet signature mismatch\nwant=%s\ngot =%s", want, got)
	}
}

func TestSignCancelMatchesSDKVector(t *testing.T) {
	priv, err := crypto.HexToECDSA("e908f86dbb4d55ac876378565aafeabc187f6690f046459397b17d9b9a19688e")
	if err != nil {
		t.Fatalf("private key: %v", err)
	}
	action := hlCancelAction{
		Type: "cancel",
		Cancels: []hlCancel{{
			Asset: 1,
			OID:   82382,
		}},
	}
	hash, err := hashAction(action, 1583838, nil)
	if err != nil {
		t.Fatalf("hashAction: %v", err)
	}
	sig, err := signL1Action(priv, hash, true)
	if err != nil {
		t.Fatalf("signL1Action: %v", err)
	}
	got := sig.R[2:] + sig.S[2:] + "1b"
	want := "02f76cc5b16e0810152fa0e14e7b219f49c361e3325f771544c6f54e157bf9fa17ed0afc11a98596be85d5cd9f86600aad515337318f7ab346e5ccc1b03425d51b"
	if got != want {
		t.Fatalf("cancel signature mismatch\nwant=%s\ngot =%s", want, got)
	}
}
