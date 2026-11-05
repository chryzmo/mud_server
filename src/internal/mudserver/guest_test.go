package mudserver

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestNewCredentialsShapeAndUniqueness(t *testing.T) {
	codeShape := regexp.MustCompile(`^[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{4}-[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{4}-[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{4}$`)
	seenTokens, seenCodes := map[string]bool{}, map[string]bool{}
	for i := 0; i < 2000; i++ {
		token, code, err := newCredentials()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil || len(raw) != 32 {
			t.Fatalf("token %q: want 32 bytes of URL-safe base64, got %d bytes, err %v", token, len(raw), err)
		}
		if !codeShape.MatchString(code) {
			t.Fatalf("recovery code %q has the wrong shape", code)
		}
		if seenTokens[token] || seenCodes[code] {
			t.Fatalf("duplicate credential after %d draws", i)
		}
		seenTokens[token], seenCodes[code] = true, true
	}
}

func TestRecoveryCodesAreCaseAndDashInsensitive(t *testing.T) {
	for _, typed := range []string{"abcd-efgh-jklm", "ABCD-EFGH-JKLM", "abcdefghjklm", "abcd efgh jklm", "ABCD-efgh-JKLM"} {
		if got := normaliseRecovery(typed); got != "ABCDEFGHJKLM" {
			t.Errorf("normaliseRecovery(%q) = %q", typed, got)
		}
	}
}

func TestHashSecretIsStableAndHidesTheSecret(t *testing.T) {
	h := hashSecret("secret-token")
	if h != hashSecret("secret-token") || h == hashSecret("secret-token2") {
		t.Error("hash must be deterministic and input-sensitive")
	}
	if len(h) != 64 || strings.Contains(h, "secret") {
		t.Errorf("hash = %q, want 64 hex characters", h)
	}
}

// canAdmit must agree with place, or join would create rows it then deletes.
func TestCanAdmitMatchesPlace(t *testing.T) {
	for _, tc := range []struct{ soft, hard, copies int }{{1, 2, 1}, {2, 3, 2}, {3, 3, 4}, {1, 1, 3}} {
		w := crowdWorldCaps(t, tc.soft, tc.hard, tc.copies)
		for i := 0; i < 40; i++ {
			for _, id := range []int32{1, 2, 3, 4, 99} {
				can := w.canAdmit(id)
				before := len(w.RoomMap)
				room, ok := w.place(id)
				if can != ok {
					t.Fatalf("soft %d hard %d copies %d, arrival %d into %d: canAdmit=%v but place ok=%v", tc.soft, tc.hard, tc.copies, i, id, can, ok)
				}
				if ok {
					room.AddPlayer(&MudPlayer{Username: fmt.Sprintf("p%d_%d", id, i)})
				} else if len(w.RoomMap) != before {
					t.Fatal("a refused placement opened a copy")
				}
			}
		}
	}
}
