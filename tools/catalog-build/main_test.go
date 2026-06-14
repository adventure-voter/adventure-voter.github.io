package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIncrementalBuild(t *testing.T) {
	catalog := t.TempDir()
	out := t.TempDir()

	git(t, catalog, "init")
	writeDeck(t, catalog, "alpha", "Alpha", "# alpha\n")
	writeDeck(t, catalog, "beta", "Beta", "# beta\n")
	git(t, catalog, "add", "-A")
	git(t, catalog, "commit", "-m", "init")

	s, err := run(catalog, out)
	if err != nil {
		t.Fatal(err)
	}
	if s.Built != 2 || s.Carried != 0 || s.Pruned != 0 {
		t.Fatalf("fresh build stats = %+v, want 2 built", s)
	}
	m := bySlug(readManifest(t, out))
	if len(m) != 2 {
		t.Fatalf("want 2 entries, got %d", len(m))
	}
	for _, slug := range []string{"alpha", "beta"} {
		e := m[slug]
		if e.TreeSHA == "" {
			t.Errorf("%s missing tree sha", slug)
		}
		if !exists(filepath.Join(out, e.Download)) {
			t.Errorf("%s download missing at %s", slug, e.Download)
		}
		if !exists(filepath.Join(out, e.Image)) {
			t.Errorf("%s cover missing at %s", slug, e.Image)
		}
	}
	alphaSHA := m["alpha"].TreeSHA

	s, err = run(catalog, out)
	if err != nil {
		t.Fatal(err)
	}
	if s.Built != 0 || s.Carried != 2 {
		t.Fatalf("unchanged rebuild stats = %+v, want 0 built 2 carried", s)
	}

	writeDeck(t, catalog, "alpha", "Alpha", "# alpha\n\nmore content\n")
	git(t, catalog, "add", "-A")
	git(t, catalog, "commit", "-m", "edit alpha")

	s, err = run(catalog, out)
	if err != nil {
		t.Fatal(err)
	}
	if s.Built != 1 || s.Carried != 1 {
		t.Fatalf("changed rebuild stats = %+v, want 1 built 1 carried", s)
	}
	m = bySlug(readManifest(t, out))
	if m["alpha"].TreeSHA == alphaSHA {
		t.Error("alpha tree sha should change after edit")
	}
	if m["beta"].Title != "Beta" {
		t.Error("beta should be carried intact")
	}

	git(t, catalog, "rm", "-r", "beta")
	git(t, catalog, "commit", "-m", "drop beta")

	s, err = run(catalog, out)
	if err != nil {
		t.Fatal(err)
	}
	if s.Pruned != 1 || s.Total != 1 {
		t.Fatalf("delete rebuild stats = %+v, want 1 pruned 1 total", s)
	}
	if exists(filepath.Join(out, "catalog", "beta")) {
		t.Error("beta directory should be pruned")
	}
	if _, ok := bySlug(readManifest(t, out))["beta"]; ok {
		t.Error("beta should be gone from manifest")
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, b)
	}
}

func writeDeck(t *testing.T, root, slug, title, body string) {
	t.Helper()
	dir := filepath.Join(root, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "index.yaml"),
		"title: "+title+"\ndescription: a sample deck\nimage: cover.svg\ntags: [demo]\n")
	mustWrite(t, filepath.Join(dir, "slides.md"), body)
	mustWrite(t, filepath.Join(dir, "cover.svg"), "<svg/>")
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readManifest(t *testing.T, out string) Manifest {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func bySlug(m Manifest) map[string]Entry {
	out := make(map[string]Entry, len(m.Presentations))
	for _, e := range m.Presentations {
		out[e.Slug] = e
	}
	return out
}
