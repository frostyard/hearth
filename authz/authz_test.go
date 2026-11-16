package authz_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/frostyard/hearth/authz"
)

// mustGrant builds a grant of items, or stops the test.
func mustGrant(t *testing.T, items ...authz.Item) authz.Grant {
	t.Helper()
	grant, err := authz.NewGrant(items...)
	if err != nil {
		t.Fatalf("NewGrant: %v", err)
	}
	return grant
}

// sel builds 1 selector value.
func sel(name, path string) authz.Sel {
	return authz.Sel{Selector: name, Path: path}
}

// TestCoverEqual checks that an item covers the path it holds.
func TestCoverEqual(t *testing.T) {
	grant := mustGrant(t, authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/work"}})
	if err := grant.Check("demo:read", sel("group", "/work")); err != nil {
		t.Errorf("Check on the item's own path: %v", err)
	}
}

// TestCoverDescendant checks that an item covers a path below it.
func TestCoverDescendant(t *testing.T) {
	grant := mustGrant(t, authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/work"}})
	for _, path := range []string{"/work/2026", "/work/2026/notes"} {
		if err := grant.Check("demo:read", sel("group", path)); err != nil {
			t.Errorf("Check on %s, below /work: %v", path, err)
		}
	}
	if err := grant.Check("demo:read", sel("group", "/workish")); err == nil {
		t.Error("Check on /workish passed, and /work covers no path that only starts with its letters")
	}
}

// TestCoverSiblingPrefix checks the sibling case: a grant on /Chan covers
// /Chan and every path below it, and never /Chani.
func TestCoverSiblingPrefix(t *testing.T) {
	grant := mustGrant(t, authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/Chan"}})
	if err := grant.Check("demo:read", sel("group", "/Chan/2026")); err != nil {
		t.Errorf("Check on /Chan/2026: %v", err)
	}
	err := grant.Check("demo:read", sel("group", "/Chani"))
	if err == nil {
		t.Fatal("Check on /Chani passed, and a grant on /Chan must not cover it")
	}
	if want := "permission denied: needs demo:read on group=/Chani"; err.Error() != want {
		t.Errorf("Check on /Chani = %q, want %q", err, want)
	}
}

// TestCoverRoot checks the root path: a grant on it covers every path, and
// only the root path covers the root itself.
func TestCoverRoot(t *testing.T) {
	root := mustGrant(t, authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/"}})
	for _, path := range []string{"/", "/work", "/work/2026", "/Chani"} {
		if err := root.Check("demo:read", sel("group", path)); err != nil {
			t.Errorf("the root grant refused %s: %v", path, err)
		}
	}
	work := mustGrant(t, authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/work"}})
	if err := work.Check("demo:read", sel("group", "/")); err == nil {
		t.Error("a grant on /work covered the root path")
	}
	if err := work.Check("demo:read", authz.Sel{Selector: "group"}); err == nil {
		t.Error("a grant on /work covered an absent selector value, which is the root")
	}
}

// TestCoverUnion checks that the grant is the union of its items: a check
// passes when 1 item covers it, and an item of another capability never helps.
func TestCoverUnion(t *testing.T) {
	grant := mustGrant(t,
		authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/work"}},
		authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/private"}},
	)
	for _, path := range []string{"/work", "/private", "/work/2026"} {
		if err := grant.Check("demo:read", sel("group", path)); err != nil {
			t.Errorf("Check on %s: %v", path, err)
		}
	}
	err := grant.Check("demo:read", sel("group", "/other"))
	if err == nil {
		t.Fatal("Check on /other passed, which no item covers")
	}
	if want := "permission denied: needs demo:read on group=/other"; err.Error() != want {
		t.Errorf("Check on /other = %q, want %q", err, want)
	}

	other := mustGrant(t, authz.Item{Cap: "demo:write", Sel: map[string]string{"group": "/work"}})
	if err := other.Check("demo:read", sel("group", "/work")); err == nil {
		t.Error("an item of demo:write covered a demo:read check")
	}

	// An item covers a check of 2 values only when it covers both.
	pair := mustGrant(t, authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/work", "folder": "/reading"}})
	if err := pair.Check("demo:read", sel("group", "/work"), sel("folder", "/reading")); err != nil {
		t.Errorf("Check of both values: %v", err)
	}
	if err := pair.Check("demo:read", sel("group", "/work"), sel("folder", "/private")); err == nil {
		t.Error("Check passed with folder=/private, which the item does not hold")
	}
}

// TestAbsentSelectorIsRoot checks the absent selector: an item that names no
// selector of the request holds the root for it, and a request that carries no
// value for a selector an item names is the root.
func TestAbsentSelectorIsRoot(t *testing.T) {
	bare := mustGrant(t, authz.Item{Cap: "demo:read"})
	if err := bare.Check("demo:read", sel("group", "/anything")); err != nil {
		t.Errorf("an item without selectors refused group=/anything: %v", err)
	}
	if err := bare.Check("demo:read"); err != nil {
		t.Errorf("a check without a selector refused an item without selectors: %v", err)
	}

	scoped := mustGrant(t, authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/work", "folder": "/reading"}})
	if err := scoped.Check("demo:read", sel("group", "/work")); err == nil {
		t.Error("an item scoped on folder=/reading covered a request that carries no folder, which is the root")
	}
	if err := scoped.Check("demo:read", sel("group", "/work"), sel("folder", "/reading")); err != nil {
		t.Errorf("both values are in the item: %v", err)
	}
	if err := bare.Check("demo:read", sel("group", "/anything"), sel("folder", "/anything")); err != nil {
		t.Errorf("an item without selectors refused 2 values: %v", err)
	}
}

// TestNFC checks that a path is compared in its Unicode NFC form, in the grant
// and in the request.
func TestNFC(t *testing.T) {
	const (
		nfc = "/Caf\u00e9"
		nfd = "/Cafe\u0301"
	)
	if nfc == nfd {
		t.Fatal("the 2 spellings of café are the same string; the test compares nothing")
	}
	for _, tc := range []struct {
		name    string
		grant   string
		request string
	}{
		{"an NFC grant and an NFD request", nfc, nfd},
		{"an NFD grant and an NFC request", nfd, nfc},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grant := mustGrant(t, authz.Item{Cap: "demo:read", Sel: map[string]string{"group": tc.grant}})
			if err := grant.Check("demo:read", sel("group", tc.request)); err != nil {
				t.Errorf("Check: %v", err)
			}
			if !grant.Allows("demo:read", "group", tc.request) {
				t.Error("Allows = false")
			}
		})
	}

	grant := mustGrant(t, authz.Item{Cap: "demo:read", Sel: map[string]string{"group": nfd}})
	if got, want := grant.Items()[0].Sel["group"], nfc; got != want {
		t.Errorf("the item's path is %q, want the NFC form %q", got, want)
	}
}

// TestRejectDotSegments checks the path rules in a grant, in a request and in
// CleanPath.
func TestRejectDotSegments(t *testing.T) {
	for _, path := range []string{"/a/.", "/a/..", "/.", "/..", "/a//b", "/a/", "", "a", "/a/./b", "/a/../b"} {
		if _, err := authz.CleanPath(path); err == nil {
			t.Errorf("CleanPath(%q) passed", path)
		}
		var pathErr *authz.PathError
		if _, err := authz.CleanPath(path); !errors.As(err, &pathErr) {
			t.Errorf("CleanPath(%q) gave %T, want *authz.PathError", path, err)
		}
		if _, err := authz.NewGrant(authz.Item{Cap: "demo:read", Sel: map[string]string{"group": path}}); err == nil {
			t.Errorf("NewGrant with the path %q passed", path)
		}
		grant := mustGrant(t)
		if path == "" {
			// An absent selector value is the root path, not an invalid path.
			if err := grant.Check("demo:read", sel("group", path)); err == nil {
				t.Error("Check with an absent value passed, and the grant holds nothing")
			}
			continue
		}
		if err := grant.Check("demo:read", sel("group", path)); !errors.As(err, &pathErr) {
			t.Errorf("Check with the path %q gave %v, want *authz.PathError", path, err)
		}
	}

	if got, err := authz.CleanPath("/a/b/c"); err != nil || got != "/a/b/c" {
		t.Errorf(`CleanPath("/a/b/c") = %q, %v`, got, err)
	}
	if got, err := authz.CleanPath("/"); err != nil || got != "/" {
		t.Errorf(`CleanPath("/") = %q, %v`, got, err)
	}
	_, err := authz.NewGrant(authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/a/../b"}})
	if err == nil {
		t.Fatal("a grant with a .. segment passed")
	}
	if want := `invalid group path "/a/../b": the segment ".." is not allowed`; !strings.Contains(err.Error(), want) {
		t.Errorf("the grant's error %q does not contain %q", err, want)
	}
}

// TestMoveCheckedAtBothEnds checks a move: the destination by the request's
// selector, the source by the hook's check of the stored object.
func TestMoveCheckedAtBothEnds(t *testing.T) {
	move := func(t *testing.T, grant authz.Grant, from, to string) error {
		t.Helper()
		// The source's value comes from the store, the destination's from the
		// request.
		if err := grant.Check("demo:write", sel("group", to)); err != nil {
			return err
		}
		return grant.Check("demo:write", sel("group", from))
	}

	source := mustGrant(t, authz.Item{Cap: "demo:write", Sel: map[string]string{"group": "/a"}})
	err := move(t, source, "/a", "/b")
	if err == nil {
		t.Fatal("the move passed, and the grant holds only the source")
	}
	if want := "permission denied: needs demo:write on group=/b"; err.Error() != want {
		t.Errorf("the refused destination = %q, want %q", err, want)
	}

	destination := mustGrant(t, authz.Item{Cap: "demo:write", Sel: map[string]string{"group": "/b"}})
	err = move(t, destination, "/a", "/b")
	if err == nil {
		t.Fatal("the move passed, and the grant holds only the destination")
	}
	if want := "permission denied: needs demo:write on group=/a"; err.Error() != want {
		t.Errorf("the refused source = %q, want %q", err, want)
	}

	both := mustGrant(t,
		authz.Item{Cap: "demo:write", Sel: map[string]string{"group": "/a"}},
		authz.Item{Cap: "demo:write", Sel: map[string]string{"group": "/b"}},
	)
	if err := move(t, both, "/a", "/b"); err != nil {
		t.Errorf("the move, with the grant on both ends: %v", err)
	}
}

// TestDeniedText checks the text of a refusal and of the other errors of the
// package.
func TestDeniedText(t *testing.T) {
	grant := mustGrant(t, authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/work"}})
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"a selector value", grant.Check("demo:read", sel("group", "/private")), "permission denied: needs demo:read on group=/private"},
		{"no selector", grant.Check("demo:write"), "permission denied: needs demo:write"},
		{"check any", grant.CheckAny("demo:write"), "permission denied: needs demo:write"},
		{"an absent value", grant.Check("demo:read", authz.Sel{Selector: "group"}), "permission denied: needs demo:read on group=/"},
	} {
		if tc.err == nil {
			t.Errorf("%s: no error", tc.name)
			continue
		}
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, got, tc.want)
		}
		var denied *authz.DeniedError
		if !errors.As(tc.err, &denied) {
			t.Errorf("%s is %T, want *authz.DeniedError", tc.name, tc.err)
		}
	}

	if _, err := authz.GrantFrom(t.Context()); err == nil {
		t.Error("GrantFrom on a context without a caller passed")
	} else if got, want := err.Error(), "permission denied: no caller"; got != want {
		t.Errorf("GrantFrom = %q, want %q", got, want)
	}
}

// TestLocalGrant checks the local user's grant: every capability on the root
// path.
func TestLocalGrant(t *testing.T) {
	// The local user's caller holds the local user's grant.
	grant := authz.LocalCaller().Grant
	if err := grant.Check("demo:read", sel("group", "/private")); err != nil {
		t.Errorf("Check: %v", err)
	}
	if err := grant.Check("anything:anywhere", sel("folder", "/x")); err != nil {
		t.Errorf("Check of a capability the app does not know: %v", err)
	}
	if err := grant.Check("demo:write"); err != nil {
		t.Errorf("Check without a selector: %v", err)
	}
	if err := grant.CheckAny("demo:publish"); err != nil {
		t.Errorf("CheckAny: %v", err)
	}
	if !grant.Allows("demo:read", "group", "/anything") {
		t.Error("Allows = false")
	}
	if err := grant.Check("demo:read", sel("group", "/a/../b")); err == nil {
		t.Error("the local grant passed an invalid path")
	}
	if items := grant.Items(); len(items) != 0 {
		t.Errorf("the local grant holds %d item(s), want none", len(items))
	}
	zero := authz.Grant{}
	if err := zero.Check("demo:read", sel("group", "/work")); err == nil {
		t.Error("the zero grant passed a check")
	}
	if err := zero.CheckAny("demo:read"); err == nil {
		t.Error("the zero grant passed CheckAny")
	}
	if err := zero.Check("demo:read"); err == nil {
		t.Error("the zero grant passed a check without a selector")
	}
}

// TestParseGrant checks the JSON form of a grant, valid and invalid.
func TestParseGrant(t *testing.T) {
	data := []byte(`[{"cap":"demo:read","sel":{"group":"/work"}},{"cap":"demo:write"}]`)
	grant, err := authz.ParseGrant(data)
	if err != nil {
		t.Fatalf("ParseGrant: %v", err)
	}
	if err := grant.Check("demo:read", sel("group", "/work/2026")); err != nil {
		t.Errorf("Check: %v", err)
	}
	if err := grant.Check("demo:write", sel("group", "/anything")); err != nil {
		t.Errorf("Check of the item without selectors: %v", err)
	}
	items := grant.Items()
	if len(items) != 2 || items[0].Cap != "demo:read" || items[0].Sel["group"] != "/work" || items[1].Cap != "demo:write" {
		t.Fatalf("Items = %+v", items)
	}

	round, err := grant.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if want := string(data); string(round) != want {
		t.Errorf("MarshalJSON = %s, want %s", round, want)
	}
	empty, err := (authz.Grant{}).MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON of the zero grant: %v", err)
	}
	if string(empty) != "[]" {
		t.Errorf("MarshalJSON of the zero grant = %s, want []", empty)
	}

	for _, invalid := range []string{
		``,
		`{}`,
		`null`,
		`[{"cap":"Demo:read"}]`,
		`[{"cap":"demo:Read"}]`,
		`[{"cap":"demo"}]`,
		`[{"cap":"demo:read","sel":{"Group":"/work"}}]`,
		`[{"cap":"demo:read","sel":{"group":"work"}}]`,
		`[{"cap":"demo:read","sel":{"group":"/work/"}}]`,
		`[{"cap":"demo:read","sel":{"group":"/a/../b"}}]`,
		`[{"cap":"demo:read","sel":{"group":"/a//b"}}]`,
		`[{"cap":"demo:read","selector":{"group":"/a"}}]`,
		`[{"cap":"demo:read"}] []`,
	} {
		if _, err := authz.ParseGrant([]byte(invalid)); err == nil {
			t.Errorf("ParseGrant(%s) passed", invalid)
		}
	}
}

// TestNewGrantChecksItems checks that new items are checked, and that the
// grant's items are copies.
func TestNewGrantChecksItems(t *testing.T) {
	if _, err := authz.NewGrant(authz.Item{Cap: "demo:read"}); err != nil {
		t.Fatalf("NewGrant: %v", err)
	}
	sel := map[string]string{"group": "/work"}
	grant := mustGrant(t, authz.Item{Cap: "demo:read", Sel: sel})
	sel["group"] = "/private"
	if err := grant.Check("demo:read", authz.Sel{Selector: "group", Path: "/work"}); err != nil {
		t.Errorf("the grant changed with the caller's map: %v", err)
	}
	grant.Items()[0].Sel["group"] = "/private"
	if err := grant.Check("demo:read", authz.Sel{Selector: "group", Path: "/work"}); err != nil {
		t.Errorf("the grant changed with the items it returned: %v", err)
	}
}

// TestParseGrantNullSel checks the fail-closed rule of a "sel" that is there: a
// null, an array, a number or a string reads as no selector, and so as the root
// path, so it is refused, while an absent "sel" stays the root.
func TestParseGrantNullSel(t *testing.T) {
	for _, invalid := range []string{
		`[{"cap":"demo:read","sel":null}]`,
		`[{"cap":"demo:read","sel":[]}]`,
		`[{"cap":"demo:read","sel":[{"group":"/work"}]}]`,
		`[{"cap":"demo:read","sel":5}]`,
		`[{"cap":"demo:read","sel":"/work"}]`,
		`[{"cap":"demo:read","sel":true}]`,
		`[{"cap":"demo:read"},{"cap":"demo:write","sel":null}]`,
	} {
		grant, err := authz.ParseGrant([]byte(invalid))
		if err == nil {
			t.Fatalf("ParseGrant(%s) passed, and a null sel would allow every path: %+v", invalid, grant.Items())
		}
		if !strings.Contains(err.Error(), `"sel" is no JSON object`) {
			t.Errorf("ParseGrant(%s) = %q, want an error that names the sel", invalid, err)
		}
	}

	// An absent "sel" is still the root path.
	grant, err := authz.ParseGrant([]byte(`[{"cap":"demo:read"},{"cap":"demo:read","sel":{}}]`))
	if err != nil {
		t.Fatalf("ParseGrant: %v", err)
	}
	for _, path := range []string{"/", "/work", "/private"} {
		if err := grant.Check("demo:read", authz.Sel{Selector: "group", Path: path}); err != nil {
			t.Errorf("Check on %s: %v", path, err)
		}
	}
}
