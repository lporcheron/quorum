package ids

import (
	"strings"
	"testing"
)

func TestGeneratedShape(t *testing.T) {
	seen := make(map[string]bool)
	for range 200 {
		id := PublicID()
		if len(id) != 12 {
			t.Fatalf("PublicID length = %d", len(id))
		}
		tok := Token()
		if len(tok) != 26 {
			t.Fatalf("Token length = %d", len(tok))
		}
		for _, s := range []string{id, tok} {
			for _, c := range s {
				if !strings.ContainsRune(alphabet, c) {
					t.Fatalf("character %q outside alphabet in %q", c, s)
				}
			}
		}
		if seen[id] || seen[tok] {
			t.Fatalf("collision after so few draws: %q", id)
		}
		seen[id], seen[tok] = true, true
	}
}

func TestMatchesHash(t *testing.T) {
	tok := Token()
	h := HashToken(tok)
	if len(h) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(h))
	}
	if !MatchesHash(tok, h) {
		t.Error("token does not match its own hash")
	}
	if MatchesHash(Token(), h) {
		t.Error("different token matched the hash")
	}
	if MatchesHash("", h) || MatchesHash(tok, "") {
		t.Error("empty input matched")
	}
}
