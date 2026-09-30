package apps

import (
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/asamgx/storix/internal/walk"
)

// FoundCLI is a product's command-line tool that is installed.
type FoundCLI struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ConfigLink is a symbolic link from where an application looks for its
// configuration to where the user keeps it.
type ConfigLink struct {
	// Path is the link, as a display path.
	Path string `json:"path"`
	// Target is where it points, resolved to an absolute display path.
	Target string `json:"target"`
}

// Reference is one line of a shell start-up file or a Brewfile.
type Reference struct {
	File string `json:"file"`
	Line int    `json:"line"`
	// Kind is "cask" or "brew" for a Brewfile entry, "shell" otherwise.
	Kind string `json:"kind"`
	// Token is the cask or formula for a Brewfile entry, and the
	// dot-directory ("/.antigravity") a shell line mentions.
	Token string `json:"token"`
	// Text is the line itself, trimmed and capped.
	Text string `json:"text,omitempty"`
}

// Describe is the evidence line for a reference.
func (r Reference) Describe() string {
	where := r.File + ":" + strconv.Itoa(r.Line)
	switch r.Kind {
	case "cask", "brew":
		return where + " still lists " + r.Kind + ` "` + r.Token + `"; ` + "`brew bundle` would install it again"
	default:
		return where + " still refers to it: " + r.Text
	}
}

// Limits on what the configuration probe reads. None of it is needed for a
// verdict, so the caps are there to keep a home with thousands of links, or
// a dotfiles repository holding a hundred Brewfiles, from costing the scan.
const (
	maxConfigLinks = 500
	maxRefFiles    = 32
	maxRefLineLen  = 160
	maxLinkHops    = 4
)

// shellFiles are the start-up files a shell reads, relative to the home.
var shellFiles = []string{
	".zshrc", ".zprofile", ".zshenv", ".zlogin",
	".bashrc", ".bash_profile", ".profile",
	".config/fish/config.fish",
}

// brewfileLine matches a Brewfile's cask and brew entries.
var brewfileLine = regexp.MustCompile(`^\s*(cask|brew)\s+["']([^"']+)["']`)

// homeDotPath matches a path to a dot-directory under the home however a
// shell file spells the home.
var homeDotPath = regexp.MustCompile(`(?:\$HOME|\$\{HOME\}|~)(/\.[A-Za-z0-9_.-]+)`)

// clis looks the product command-line tools up on the PATH.
func (p *prober) clis() {
	if p.env.LookPath == nil {
		return
	}
	for _, prod := range defaultIndex.all {
		for _, cli := range prod.CLIs {
			if full, err := p.env.LookPath(cli.Name); err == nil {
				p.f.CLIs = append(p.f.CLIs, FoundCLI{Name: cli.Name, Path: p.display(full)})
			}
		}
	}
}

// configLinks lists the symbolic links where applications keep their
// configuration: directly in the home, in ~/.config, and in the "User"
// folder an editor built on VS Code keeps under Application Support.
func (p *prober) configLinks() {
	if p.env.ReadDir == nil || p.env.Readlink == nil {
		return
	}
	home := p.paths.Home
	dirs := []string{home, path.Join(home, ".config")}
	support := path.Join(home, "Library/Application Support")
	if names, err := p.readDir(support); err == nil {
		for _, n := range names {
			dirs = append(dirs, path.Join(support, n, "User"))
		}
	}
	for _, dir := range dirs {
		entries, err := p.env.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Type()&fs.ModeSymlink == 0 {
				continue
			}
			full := path.Join(dir, e.Name())
			target, err := p.env.Readlink(full)
			if err != nil {
				continue
			}
			if !path.IsAbs(target) {
				target = path.Join(dir, target)
			}
			p.f.ConfigLinks = append(p.f.ConfigLinks, ConfigLink{
				Path: p.display(full), Target: p.display(path.Clean(target)),
			})
			if len(p.f.ConfigLinks) >= maxConfigLinks {
				return
			}
		}
	}
}

// references reads the shell start-up files and the Brewfiles.
//
// A Brewfile has no fixed place. Besides the two Homebrew documents, the
// probe looks in the repositories the configuration links point into — a
// dotfiles repository is where a Brewfile per machine usually lives — at the
// top level and one directory down.
func (p *prober) references() {
	if p.env.ReadFile == nil {
		return
	}
	home := p.paths.Home
	var files []string
	for _, rel := range shellFiles {
		files = append(files, path.Join(home, rel))
	}
	files = append(files, path.Join(home, "Brewfile"), path.Join(home, ".Brewfile"))
	files = append(files, p.repoBrewfiles()...)

	seen := make(map[string]bool)
	for _, f := range files {
		if len(seen) >= maxRefFiles {
			return
		}
		resolved, data, ok := p.readFollowing(f)
		if !ok || seen[resolved] {
			continue
		}
		seen[resolved] = true
		p.scanLines(p.display(f), string(data), path.Base(resolved))
	}
}

// repoBrewfiles finds the Brewfiles in the repositories the configuration
// links point into.
func (p *prober) repoBrewfiles() []string {
	home := p.display(p.paths.Home)
	repos := make(map[string]bool)
	var order []string
	for _, l := range p.f.ConfigLinks {
		rel, ok := strings.CutPrefix(l.Target, home+"/")
		if !ok || strings.HasPrefix(rel, "Library/") {
			continue
		}
		top := strings.SplitN(rel, "/", 2)[0]
		if top == "" || repos[top] {
			continue
		}
		repos[top] = true
		order = append(order, top)
	}
	var out []string
	for _, top := range order {
		repo := path.Join(p.paths.Home, top)
		out = append(out, path.Join(repo, "Brewfile"))
		names, err := p.readDir(repo)
		if err != nil {
			continue
		}
		for _, n := range names {
			if strings.HasPrefix(n, ".") {
				continue
			}
			out = append(out, path.Join(repo, n, "Brewfile"))
		}
	}
	return out
}

// readFollowing reads a file through up to maxLinkHops symbolic links. The
// sanctioned reader refuses a link, which is right for everything else it
// reads, but a shell file kept in a dotfiles repository is a link by design.
func (p *prober) readFollowing(file string) (string, []byte, bool) {
	cur := file
	for range maxLinkHops {
		data, err := p.env.ReadFile(cur)
		if err == nil {
			return cur, data, true
		}
		if p.env.Readlink == nil {
			return "", nil, false
		}
		target, lerr := p.env.Readlink(cur)
		if lerr != nil {
			return "", nil, false
		}
		if !path.IsAbs(target) {
			target = path.Join(path.Dir(cur), target)
		}
		cur = path.Clean(target)
	}
	return "", nil, false
}

// scanLines keeps the lines of one file that could name an application.
func (p *prober) scanLines(display, data, base string) {
	brewfile := base == "Brewfile" || base == ".Brewfile"
	for i, line := range strings.Split(data, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if brewfile {
			if m := brewfileLine.FindStringSubmatch(trimmed); m != nil {
				p.f.References = append(p.f.References, Reference{
					File: display, Line: i + 1, Kind: m[1], Token: m[2],
				})
			}
			continue
		}
		for _, m := range homeDotPath.FindAllStringSubmatch(trimmed, -1) {
			text := trimmed
			if len(text) > maxRefLineLen {
				text = text[:maxRefLineLen] + "…"
			}
			p.f.References = append(p.f.References, Reference{
				File: display, Line: i + 1, Kind: "shell", Token: firstSegment(m[1]), Text: text,
			})
		}
	}
}

// firstSegment is "/.antigravity" for "/.antigravity/antigravity/bin".
func firstSegment(p string) string {
	if i := strings.IndexByte(p[1:], '/'); i >= 0 {
		return p[:i+1]
	}
	return p
}

// attachConfig adds to each verdict what the configuration probe found
// about the owner: a command-line tool that still uses part of its data, the
// user's own configuration linked into that data, and the start-up files and
// Brewfiles that still name it.
//
// Only an owner the report offers for removal needs any of it. An installed
// application listed in a Brewfile is the Brewfile working as intended.
func (a *Analysis) attachConfig(f *Facts) {
	if f == nil {
		return
	}
	for _, key := range a.OwnerKeys() {
		o, v := a.Owners[key], a.Verdicts[key]
		if v == nil || (!v.State.Reclaimable() && v.State != StateUnknown) {
			continue
		}
		prod, _ := defaultIndex.LookupSlug(o.Owner.Slug)
		a.attachCLIs(o, v, prod, f)
		a.attachLinks(o, v, prod, f)
		a.attachReferences(o, v, prod, f)
	}
}

// attachCLIs protects the directories an installed product CLI still uses.
func (a *Analysis) attachCLIs(o *OwnerResult, v *Verdict, prod *Product, f *Facts) {
	if prod == nil {
		return
	}
	for _, cli := range prod.CLIs {
		for _, found := range f.CLIs {
			if found.Name != cli.Name {
				continue
			}
			for _, use := range cli.Uses {
				dir := a.Inventory.Paths.Resolve(use)
				v.Protected = append(v.Protected, Protection{Path: dir,
					Reason: "the " + cli.Name + " command-line tool is still installed at " + found.Path + " and reads it"})
				v.Evidence = append(v.Evidence, "the "+cli.Name+" command-line tool is still installed at "+
					found.Path+", so "+dir+" is kept for it; the rest of "+o.Owner.Label+" is gone")
			}
		}
	}
}

// attachLinks records the user's configuration linked into the owner's data.
func (a *Analysis) attachLinks(o *OwnerResult, v *Verdict, prod *Product, f *Facts) {
	dots := dotNames(prod)
	for _, l := range f.ConfigLinks {
		if !a.linkBelongs(o, l, dots) {
			continue
		}
		v.Links = append(v.Links, l)
		v.Evidence = append(v.Evidence, l.Path+" is a link into "+l.Target+
			": your own configuration, not a leftover; removing the link leaves "+l.Target+" alone")
	}
}

// linkBelongs reports whether a configuration link is one of the owner's:
// inside one of its directories, or named after its dot-directory.
func (a *Analysis) linkBelongs(o *OwnerResult, l ConfigLink, dots []string) bool {
	for _, i := range o.Members {
		if strings.HasPrefix(l.Path, a.Candidates[i].Path+"/") {
			return true
		}
	}
	base := path.Base(l.Path)
	for _, d := range dots {
		if strings.EqualFold(base, d) && path.Dir(l.Path) == a.Inventory.Paths.Home {
			return true
		}
	}
	return false
}

// attachReferences records the Brewfile entries and shell lines that name
// the owner.
func (a *Analysis) attachReferences(o *OwnerResult, v *Verdict, prod *Product, f *Facts) {
	casks := make(map[string]bool)
	formulae := make(map[string]bool)
	dots := make(map[string]bool)
	if prod != nil {
		for _, c := range prod.Casks {
			casks[c] = true
		}
		for _, cli := range prod.CLIs {
			formulae[cli.Name] = true
		}
		for _, d := range dotNames(prod) {
			dots["/"+strings.ToLower(d)] = true
		}
	}
	if token, ok := strings.CutPrefix(o.Owner.Key, "cask:"); ok {
		casks[token] = true
	}
	for _, r := range f.References {
		hit := false
		switch r.Kind {
		case "cask":
			hit = casks[r.Token]
		case "brew":
			hit = formulae[r.Token]
		case "shell":
			hit = dots[strings.ToLower(r.Token)]
		}
		if hit {
			v.References = append(v.References, r)
			v.Evidence = append(v.Evidence, r.Describe())
		}
	}
}

// dotNames are the product's dot-directory names: ".cursor", ".warp".
func dotNames(prod *Product) []string {
	if prod == nil {
		return nil
	}
	var out []string
	for _, n := range prod.Names {
		if strings.HasPrefix(n, ".") {
			out = append(out, n)
		}
	}
	return out
}

// Protection is a directory an owner's verdict keeps although the owner is
// gone, and why.
type Protection struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// protectedNodes maps each kept directory's node to why it is kept.
func (a *Analysis) protectedNodes() map[*walk.Node]string {
	out := make(map[*walk.Node]string)
	for _, v := range a.Verdicts {
		for _, pr := range v.Protected {
			if n, ok := lookupDisplay(a.Tree, pr.Path); ok {
				out[n] = pr.Reason
			}
		}
	}
	return out
}
