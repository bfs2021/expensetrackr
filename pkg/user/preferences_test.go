package user

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nutmos/expensetrackr/pkg/validate"
)

func TestDecodePreferences(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		want      Preferences
		wantField string // "" = valid
	}{
		{"absent", ``, Preferences{}, ""},
		{"null", `null`, Preferences{}, ""},
		{"empty object", `{}`, Preferences{}, ""},
		{"base currency", `{"base_currency":"EUR"}`, Preferences{BaseCurrency: "EUR"}, ""},
		{"normalized code", `{ "base_currency" : " jpy " }`, Preferences{BaseCurrency: "JPY"}, ""},
		{"empty code means default", `{"base_currency":""}`, Preferences{}, ""},
		{"unknown currency", `{"base_currency":"ABC"}`, Preferences{}, "base_currency"},
		{"testing code excluded", `{"base_currency":"XTS"}`, Preferences{}, "base_currency"},
		{"wrong type", `{"base_currency":42}`, Preferences{}, "base_currency"},
		{"unknown key", `{"theme":"dark"}`, Preferences{}, "preferences"},
		{"array", `[1,2]`, Preferences{}, "preferences"},
		{"string", `"x"`, Preferences{}, "preferences"},
		{"number", `42`, Preferences{}, "preferences"},
		{"too big", `{"base_currency":"` + strings.Repeat("x", MaxPreferencesSize) + `"}`, Preferences{}, "preferences"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodePreferences(json.RawMessage(tc.in))
			if tc.wantField == "" {
				if err != nil || got != tc.want {
					t.Fatalf("got %+v, %v; want %+v", got, err, tc.want)
				}
				return
			}
			var ve *validate.ValidationError
			if !errors.As(err, &ve) || ve.Fields[tc.wantField] == "" {
				t.Fatalf("want validation error on %q, got %v", tc.wantField, err)
			}
		})
	}
}

func TestParseStoredPreferences(t *testing.T) {
	tests := []struct {
		name    string
		stored  string
		want    Preferences
		wantErr bool
	}{
		{"empty column", ``, Preferences{}, false},
		{"empty object", `{}`, Preferences{}, false},
		{"set", `{"base_currency":"SGD"}`, Preferences{BaseCurrency: "SGD"}, false},
		{"unknown keys ignored", `{"theme":"dark","base_currency":"THB","x":{"y":[1]}}`, Preferences{BaseCurrency: "THB"}, false},
		{"corrupt", `{bad`, Preferences{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseStoredPreferences([]byte(tc.stored))
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}

func TestPreferencesJSON(t *testing.T) {
	tests := []struct {
		name       string
		p          Preferences
		wantAPI    string
		wantStored string
	}{
		{"default", Preferences{}, `{"base_currency":"` + DefaultBaseCurrency + `"}`, `{}`},
		{"set", Preferences{BaseCurrency: "EUR"}, `{"base_currency":"EUR"}`, `{"base_currency":"EUR"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api, err := json.Marshal(tc.p)
			if err != nil || string(api) != tc.wantAPI {
				t.Errorf("API JSON %s %v, want %s", api, err, tc.wantAPI)
			}
			if got := tc.p.StoredJSON(); got != tc.wantStored {
				t.Errorf("stored %s, want %s", got, tc.wantStored)
			}
		})
	}
}

func TestDefaultBaseCurrencySupported(t *testing.T) {
	if _, err := (Preferences{BaseCurrency: DefaultBaseCurrency}).Normalize(); err != nil {
		t.Fatalf("default %s is not a supported currency: %v", DefaultBaseCurrency, err)
	}
}
