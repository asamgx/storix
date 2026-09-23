// Package docker detects Docker Desktop.
//
// It is gated on Docker Desktop's own files, never on a docker context or on
// the presence of the docker CLI. On the machine storix was written against,
// `docker` is installed, `/var/run/docker.sock` exists and a context called
// "default" points at it — and every one of those belongs to OrbStack. A
// detector that inferred Docker Desktop from any of them would invent a second
// container runtime and count the same disk image twice.
//
// Docker Desktop is not installed on that machine, so this detector ships
// unverified: its fixtures are written from Docker's documented output rather
// than recorded from a daemon, and its Status says so.
package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/asamgx/storix/internal/classify"
	"github.com/asamgx/storix/internal/detect"
	"github.com/asamgx/storix/internal/probe"
	"github.com/asamgx/storix/internal/units"
	"github.com/asamgx/storix/internal/walk"
)

// Name is the detector's identifier.
const Name = "docker"

// bundle and container are the two files whose existence means Docker Desktop
// is installed: the application, and the sandbox container holding its disk
// image. Either one alone is enough, because an application deleted without
// its data is exactly the case the ledger has to explain.
const (
	bundle    = "/Applications/Docker.app"
	container = "Library/Containers/com.docker.docker"
)

// ownerKeys are the identifiers an application footprint joins on.
var ownerKeys = []string{"app:com.docker.docker"}

// dfTimeout is what `docker system df` is allowed.
const dfTimeout = 10 * time.Second

// desktopContexts are the context names Docker Desktop creates. They are a
// hint for choosing between several contexts once the gate has already proved
// Desktop is installed, never a reason to decide that it is.
var desktopContexts = []string{"desktop-linux", "default"}

// desktopSocketMark identifies a Docker Desktop socket by path.
const desktopSocketMark = "com.docker"

func init() { detect.Register(20, New()) }

// Detector finds Docker Desktop.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Unverified reports that this detector has never been run against a machine
// with Docker Desktop on it.
func (*Detector) Unverified() bool { return true }

// Row is one line of what the daemon says it is using.
type Row struct {
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	Reclaimable int64  `json:"reclaimable"`
	Percent     int    `json:"percent,omitempty"`
	PercentOK   bool   `json:"percent_known,omitempty"`
	Count       int    `json:"count"`
	Active      int    `json:"active"`
}

// Facts are what the probe learned.
type Facts struct {
	// Bundle is where the application was found, empty when only its data
	// is left.
	Bundle string `json:"bundle,omitempty"`
	// Context is the docker context the daemon was queried through.
	Context string `json:"context,omitempty"`
	// Endpoint is that context's socket.
	Endpoint string `json:"endpoint,omitempty"`
	// DF is the daemon's own accounting.
	DF []Row `json:"df,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// Probe gates on Docker Desktop's files and then, only then, asks its daemon.
func (*Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	f := &Facts{}
	hasBundle := env.Exists(bundle)
	hasData := env.Exists(path.Join(env.Home, container))
	switch {
	case hasBundle:
		f.Bundle = bundle
	case hasData:
	default:
		return nil, detect.Missingf("no %s and no %s container", bundle, path.Base(container))
	}

	if !env.Has("docker") {
		return f, detect.Degradedf("the docker CLI is not on the path; the host disk image only")
	}
	name, endpoint, err := findContext(ctx, env.Runner)
	if err != nil {
		return f, detect.Degradedf("%s; the host disk image only", err)
	}
	f.Context, f.Endpoint = name, endpoint

	rows, err := systemDF(ctx, env.Runner, name)
	if err != nil {
		return f, detect.Degradedf("daemon not running; the host disk image only (%s)", err)
	}
	f.DF = rows
	return f, nil
}

// dockerContext is one line of `docker context ls --format json`.
type dockerContext struct {
	Name           string `json:"Name"`
	DockerEndpoint string `json:"DockerEndpoint"`
	Current        bool   `json:"Current"`
	Error          string `json:"Error"`
}

// findContext picks the context that talks to Docker Desktop: one whose
// socket lives inside Desktop's container, else one of Desktop's own context
// names. A context whose socket belongs to another runtime is skipped, so a
// machine with both Desktop and OrbStack reports each against its own daemon.
func findContext(ctx context.Context, r probe.Runner) (name, endpoint string, err error) {
	res := r.Run(ctx, probe.Cmd{Name: "docker", Args: []string{"context", "ls", "--format", "json"}})
	if !res.OK() {
		return "", "", fmt.Errorf("docker context ls: %s", res.Reason())
	}
	list := parseContexts(res.Stdout)
	for _, c := range list {
		if strings.Contains(c.DockerEndpoint, desktopSocketMark) {
			return c.Name, c.DockerEndpoint, nil
		}
	}
	for _, want := range desktopContexts {
		for _, c := range list {
			if c.Name == want && !foreignSocket(c.DockerEndpoint) {
				return c.Name, c.DockerEndpoint, nil
			}
		}
	}
	return "", "", errors.New("no docker context points at Docker Desktop")
}

// foreignSockets are the sockets of the other runtimes storix knows about. A
// context pointing at one of them is not Docker Desktop's whatever it is
// called, which is what keeps "default" from being claimed twice.
var foreignSockets = []string{".orbstack/", ".colima/", ".lima/", "podman"}

// foreignSocket reports whether an endpoint belongs to another runtime.
func foreignSocket(endpoint string) bool {
	for _, mark := range foreignSockets {
		if strings.Contains(endpoint, mark) {
			return true
		}
	}
	return false
}

// parseContexts reads the one-object-per-line form, or the array older CLIs
// print.
func parseContexts(out string) []dockerContext {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	if strings.HasPrefix(out, "[") {
		var arr []dockerContext
		if json.Unmarshal([]byte(out), &arr) == nil {
			return arr
		}
		return nil
	}
	var list []dockerContext
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c dockerContext
		if json.Unmarshal([]byte(line), &c) == nil && c.Name != "" {
			list = append(list, c)
		}
	}
	return list
}

// dfRow is one line of `docker system df --format json`; every value is a
// human string even inside the JSON.
type dfRow struct {
	Type        string `json:"Type"`
	TotalCount  string `json:"TotalCount"`
	Active      string `json:"Active"`
	Size        string `json:"Size"`
	Reclaimable string `json:"Reclaimable"`
}

// systemDF asks the daemon what it believes it is using.
func systemDF(ctx context.Context, r probe.Runner, dockerCtx string) ([]Row, error) {
	res := r.Run(ctx, probe.Cmd{
		Name:    "docker",
		Args:    []string{"--context", dockerCtx, "system", "df", "--format", "json"},
		Timeout: dfTimeout,
	})
	if !res.OK() {
		return nil, errors.New(res.Reason())
	}
	rows := parseDF(res.Stdout)
	if len(rows) == 0 {
		return nil, errors.New("docker system df returned nothing readable")
	}
	return rows, nil
}

// parseDF reads the df table, one JSON object per line.
func parseDF(out string) []Row {
	var rows []Row
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var raw dfRow
		if json.Unmarshal([]byte(line), &raw) != nil || raw.Type == "" {
			continue
		}
		size, _ := probe.ParseHumanBytes(raw.Size)
		reclaimable, _ := probe.ParseHumanBytes(raw.Reclaimable)
		pct, pctOK := probe.ParsePercent(raw.Reclaimable)
		rows = append(rows, Row{
			Type: raw.Type, Size: size, Reclaimable: reclaimable,
			Percent: pct, PercentOK: pctOK,
			Count: probe.ParseCount(raw.TotalCount), Active: probe.ParseCount(raw.Active),
		})
	}
	return rows
}

// Classify claims Docker Desktop's directories and builds its runtime row.
//
// Nothing is claimed when the gate never opened: a machine without Docker
// Desktop has none of these paths, and emitting a runtime for it would put an
// empty second row under Containers beside the one that is really there.
func (*Detector) Classify(t *walk.Tree, f detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	facts, _ := f.(*Facts)
	home := cx.Home
	if facts == nil || home == "" {
		// The gate never opened: Docker Desktop is not installed, and
		// ~/.docker on such a machine belongs to whichever CLI is
		// there, which here is OrbStack's. The catalog rule buckets it.
		return nil, detect.Summary{}
	}

	evidence := evidenceLines(facts)
	targets := []detect.Target{
		{
			Path: path.Join(home, container), Category: "Docker Desktop", Kind: "image",
			Reclaim: classify.ToolManaged,
			Explain: "Docker Desktop's virtual machine disk (Docker.raw); `docker system prune` frees space inside it",
		},
		{
			Path: path.Join(home, "Library/Group Containers/group.com.docker"), Category: "Docker Desktop", Kind: "data",
			Reclaim: classify.ToolManaged, Explain: "Docker Desktop's shared settings and caches",
		},
		{
			Path: path.Join(home, ".docker"), Category: "Docker Desktop", Kind: "cache",
			Reclaim: classify.Regenerable,
			Explain: "the Docker CLI's configuration, contexts, buildx state and Scout cache",
		},
	}
	for i := range targets {
		targets[i].Bucket = classify.BucketContainers
		targets[i].Owner = "Docker Desktop"
		targets[i].OwnerKeys = ownerKeys
		targets[i].Evidence = evidence
	}

	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	return claims, detect.Summary{Tools: tools, Runtimes: []detect.Runtime{runtime(t, facts, home, tools)}}
}

// rawImage is where Docker Desktop keeps the guest's whole disk.
const rawImage = "Library/Containers/com.docker.docker/Data/vms/0/data/Docker.raw"

// runtime builds the host-versus-guest view.
func runtime(t *walk.Tree, f *Facts, home string, tools []detect.Tool) detect.Runtime {
	rt := detect.Runtime{Name: "Docker Desktop"}
	if n, ok := detect.Lookup(t, path.Join(home, rawImage)); ok {
		note := "the guest's disk image, sparse"
		if n.Apparent > n.Bytes {
			note = fmt.Sprintf("%s apparent, %s allocated", units.Decimal.Bytes(n.Apparent), units.Decimal.Bytes(n.Bytes))
		}
		rt.HostImage = append(rt.HostImage, detect.Tool{
			Name: "Docker.raw", Kind: "image", Path: n.Display(), Node: n.ID,
			Bytes: n.Bytes, Reclaim: classify.ToolManaged, Note: note,
		})
	} else {
		// Docker.raw is not always where it is documented to be; the
		// container directory as a whole is then the honest number.
		for _, tool := range tools {
			if strings.HasSuffix(tool.Path, container) {
				t := tool
				t.Kind = "image"
				t.Note = "the whole sandbox container; Docker.raw was not where it was looked for"
				rt.HostImage = append(rt.HostImage, t)
			}
		}
	}
	if f == nil {
		rt.Note = "Docker Desktop did not answer; the host disk image is all that could be measured"
		return rt
	}

	rt.Context = f.Context
	for _, row := range f.DF {
		rt.GuestReported = append(rt.GuestReported, detect.Line{
			Type: row.Type, Size: row.Size, Reclaimable: row.Reclaimable,
			Percent: row.Percent, Known: row.PercentOK,
			Count: row.Count, Active: row.Active,
			Reclaim: rowReclaim(row.Type),
		})
	}
	rt.Note = note(rt, f)
	return rt
}

// rowReclaim is how safely a df row's reclaimable share could be freed.
func rowReclaim(rowType string) classify.Reclaim {
	if strings.Contains(strings.ToLower(rowType), "volume") {
		return classify.UserData
	}
	return classify.ToolManaged
}

// note explains the gap between the two columns.
func note(rt detect.Runtime, f *Facts) string {
	if len(f.DF) == 0 {
		return "the Docker Desktop daemon is not running, so only the host's side of the image is known"
	}
	host, guest := rt.HostBytes(), rt.GuestBytes()
	u := units.Decimal
	if host >= guest {
		return fmt.Sprintf("the host has given Docker.raw %s and the daemon accounts for %s of it; "+
			"Docker Desktop does not shrink the image on its own, so the %s difference stays until the disk image is compacted",
			u.Bytes(host), u.Bytes(guest), u.Bytes(host-guest))
	}
	return fmt.Sprintf("the daemon accounts for %s inside an image the host has given %s",
		u.Bytes(guest), u.Bytes(host))
}

// evidenceLines are the why-panel lines every Docker Desktop claim carries.
func evidenceLines(f *Facts) []string {
	if f == nil {
		return []string{"docker did not answer; the paths come from the static catalog"}
	}
	var out []string
	if f.Bundle != "" {
		out = append(out, f.Bundle+" exists")
	}
	if f.Context != "" {
		out = append(out, "docker context "+f.Context+" → "+f.Endpoint)
	}
	for _, row := range f.DF {
		out = append(out, fmt.Sprintf("`docker system df`: %s %s, %s reclaimable, %d of %d active",
			row.Type, units.Decimal.Bytes(row.Size), units.Decimal.Bytes(row.Reclaimable), row.Active, row.Count))
	}
	return out
}
