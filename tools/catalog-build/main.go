package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Index struct {
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Image       string   `yaml:"image"`
	Tags        []string `yaml:"tags"`
	Location    string   `yaml:"location"`
}

type Entry struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Image       string   `json:"image"`
	Download    string   `json:"download"`
	Size        int64    `json:"size"`
	Updated     string   `json:"updated"`
	Tags        []string `json:"tags"`
	TreeSHA     string   `json:"treeSha"`
}

type Manifest struct {
	Generated     string  `json:"generated"`
	Presentations []Entry `json:"presentations"`
}

func main() {
	catalog := flag.String("catalog", "", "path to the cloned catalog repository")
	out := flag.String("out", ".", "website root to write manifest and artifacts into")
	flag.Parse()

	if *catalog == "" {
		fmt.Fprintln(os.Stderr, "error: -catalog is required")
		os.Exit(1)
	}
	s, err := run(*catalog, *out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("catalog: %d presentations (%d built, %d unchanged, %d pruned)\n",
		s.Total, s.Built, s.Carried, s.Pruned)
}

type stats struct {
	Total, Built, Carried, Pruned int
}

func run(catalog, out string) (stats, error) {
	prior, err := loadPrior(filepath.Join(out, "manifest.json"))
	if err != nil {
		return stats{}, err
	}

	slugs, err := presentationDirs(catalog)
	if err != nil {
		return stats{}, err
	}

	var entries []Entry
	var s stats
	seen := make(map[string]bool, len(slugs))
	for _, slug := range slugs {
		seen[slug] = true
		tree, err := treeSHA(catalog, slug)
		if err != nil {
			return stats{}, fmt.Errorf("tree sha for %s: %w", slug, err)
		}

		tarPath := filepath.Join(out, "catalog", slug, slug+".tar.gz")
		if p, ok := prior[slug]; ok && p.TreeSHA == tree && exists(tarPath) && exists(filepath.Join(out, p.Image)) {
			entries = append(entries, p)
			s.Carried++
			continue
		}

		e, err := buildDeck(catalog, out, slug, tree)
		if err != nil {
			return stats{}, fmt.Errorf("build %s: %w", slug, err)
		}
		entries = append(entries, e)
		s.Built++
	}

	for slug := range prior {
		if !seen[slug] {
			if err := os.RemoveAll(filepath.Join(out, "catalog", slug)); err != nil {
				return stats{}, fmt.Errorf("prune %s: %w", slug, err)
			}
			s.Pruned++
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Updated != entries[j].Updated {
			return entries[i].Updated > entries[j].Updated
		}
		return entries[i].Slug < entries[j].Slug
	})

	if err := writeManifest(filepath.Join(out, "manifest.json"), entries); err != nil {
		return stats{}, err
	}

	s.Total = len(entries)
	return s, nil
}

func loadPrior(path string) (map[string]Entry, error) {
	prior := map[string]Entry{}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return prior, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parse prior manifest: %w", err)
	}
	for _, e := range m.Presentations {
		prior[e.Slug] = e
	}
	return prior, nil
}

func presentationDirs(root string) ([]string, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var slugs []string
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if exists(filepath.Join(root, e.Name(), "index.yaml")) {
			slugs = append(slugs, e.Name())
		}
	}
	sort.Strings(slugs)
	return slugs, nil
}

func buildDeck(catalog, out, slug, tree string) (Entry, error) {
	src := filepath.Join(catalog, slug)

	var idx Index
	b, err := os.ReadFile(filepath.Join(src, "index.yaml"))
	if err != nil {
		return Entry{}, err
	}
	if err := yaml.Unmarshal(b, &idx); err != nil {
		return Entry{}, fmt.Errorf("parse index.yaml: %w", err)
	}
	if idx.Title == "" {
		idx.Title = slug
	}
	if idx.Location == "" {
		return Entry{}, fmt.Errorf("index.yaml: location is required")
	}

	content := filepath.Join(src, idx.Location)
	info, err := os.Stat(content)
	if err != nil {
		return Entry{}, fmt.Errorf("location %q: %w", idx.Location, err)
	}
	if !info.IsDir() {
		return Entry{}, fmt.Errorf("location %q is not a directory", idx.Location)
	}

	dest := filepath.Join(out, "catalog", slug)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return Entry{}, err
	}

	download := path.Join("catalog", slug, slug+".tar.gz")
	size, err := writeTar(content, filepath.Join(out, download))
	if err != nil {
		return Entry{}, err
	}

	var image string
	if idx.Image != "" {
		ext := filepath.Ext(idx.Image)
		rel := path.Join("catalog", slug, "cover"+ext)
		if err := copyFile(filepath.Join(src, idx.Image), filepath.Join(out, rel)); err != nil {
			return Entry{}, fmt.Errorf("copy cover: %w", err)
		}
		image = rel
	}

	updated := strings.TrimSpace(gitOut(catalog, "log", "-1", "--format=%cI", "--", slug))
	if updated == "" {
		updated = time.Now().UTC().Format(time.RFC3339)
	}

	return Entry{
		Slug:        slug,
		Title:       idx.Title,
		Description: idx.Description,
		Image:       image,
		Download:    download,
		Size:        size,
		Updated:     updated,
		Tags:        idx.Tags,
		TreeSHA:     tree,
	}, nil
}

func writeTar(src, dst string) (int64, error) {
	var rels []string
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		rels = append(rels, rel)
		return nil
	})
	if err != nil {
		return 0, err
	}
	sort.Strings(rels)

	f, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, rel := range rels {
		full := filepath.Join(src, rel)
		info, err := os.Stat(full)
		if err != nil {
			return 0, err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return 0, err
		}
		hdr.Name = filepath.ToSlash(rel)
		hdr.ModTime = time.Unix(0, 0)
		if err := tw.WriteHeader(hdr); err != nil {
			return 0, err
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return 0, err
		}
		if _, err := tw.Write(data); err != nil {
			return 0, err
		}
	}
	if err := tw.Close(); err != nil {
		return 0, err
	}
	if err := gz.Close(); err != nil {
		return 0, err
	}
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func writeManifest(path string, entries []Entry) error {
	if entries == nil {
		entries = []Entry{}
	}
	m := Manifest{
		Generated:     time.Now().UTC().Format(time.RFC3339),
		Presentations: entries,
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func treeSHA(catalog, slug string) (string, error) {
	cmd := exec.Command("git", "-C", catalog, "rev-parse", "HEAD:"+slug)
	b, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func gitOut(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	b, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(b)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
