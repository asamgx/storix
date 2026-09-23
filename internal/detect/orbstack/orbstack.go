// Package orbstack detects OrbStack, the Linux machine and Docker runtime
// this machine uses.
//
// OrbStack keeps everything in one sparse disk image inside its group
// container, so the host number and the guest number are different questions
// with different answers: the host has given the image 18.8 GB of real blocks
// while the daemon inside it believes it is using 20 GB across images,
// containers, volumes and build cache. Both are true, the gap is OrbStack's
// automatic shrinking keeping up, and the containers view shows the two side
// by side rather than picking one.
package orbstack

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
const Name = "orbstack"

// group is the group container OrbStack stores its disk image in. The team
// id is OrbStack's own and is stable across installations.
const group = "Library/Group Containers/HUAQ24HBR6.dev.orbstack"

// ownerKeys are the identifiers an application footprint joins OrbStack's
// bytes on: the app itself, the helper bundle that actually owns the group
// container, and the team the container is named for.
var ownerKeys = []string{"app:dev.orbstack.OrbStack", "app:dev.kdrag0n.MacVirt", "team:HUAQ24HBR6"}

// dfTimeout is what `docker system df` is allowed; it queries a daemon over a
// local socket, so ten seconds is already generous.
const dfTimeout = 10 * time.Second

// maxDetail is how much of the verbose `system df -v` table is kept as
// evidence. It is only ever read by a person in the why panel.
const maxDetail = 16 << 10

func init() { detect.Register(10, New()) }

// Detector finds OrbStack.
type Detector struct{}

// New returns the detector.
func New() *Detector { return &Detector{} }

// Name implements detect.Detector.
func (*Detector) Name() string { return Name }

// NewFacts implements detect.Detector.
func (*Detector) NewFacts() detect.Facts { return &Facts{} }

// Machine is one Linux machine OrbStack manages.
type Machine struct {
	Name  string `json:"name"`
	State string `json:"state,omitempty"`
	Image string `json:"image,omitempty"`
}

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
	// Machines are the Linux machines; empty is the ordinary case for a
	// machine used only for Docker.
	Machines []Machine `json:"machines,omitempty"`
	// Context is the docker context the daemon was queried through.
	Context string `json:"context,omitempty"`
	// Endpoint is that context's socket, which is the evidence that the
	// context really is OrbStack's and not Docker Desktop's.
	Endpoint string `json:"endpoint,omitempty"`
	// DF is the daemon's own accounting.
	DF []Row `json:"df,omitempty"`
	// Detail is the verbose per-image table, kept verbatim for the why
	// panel and truncated to maxDetail.
	Detail string `json:"detail,omitempty"`
}

// Kind implements detect.Facts.
func (*Facts) Kind() string { return Name }

// Probe asks orb and docker what they know.
//
// The gate is presence, not health: OrbStack is installed if `orb` is on the
// path or its directories exist, and everything after that is evidence that
// may or may not be gathered. A stopped OrbStack is Degraded with the host
// numbers intact, never Missing, because the disk image is still on the disk.
func (*Detector) Probe(ctx context.Context, env detect.Env) (detect.Facts, error) {
	home := env.Home
	installed := env.Has("orb") ||
		env.Exists(path.Join(home, ".orbstack")) ||
		env.Exists(path.Join(home, group))
	if !installed {
		return nil, detect.Missingf("neither `orb` on the path nor ~/.orbstack")
	}

	f := &Facts{}
	var degraded []string

	if env.Has("orb") {
		if machines, err := listMachines(ctx, env.Runner); err != nil {
			degraded = append(degraded, err.Error())
		} else {
			f.Machines = machines
		}
	}

	if !env.Has("docker") {
		return f, detect.Degradedf("docker is not on the path; the host disk image only")
	}
	name, endpoint, err := findContext(ctx, env.Runner)
	if err != nil {
		return f, detect.Degradedf("%s; the host disk image only", err)
	}
	f.Context, f.Endpoint = name, endpoint

	rows, err := systemDF(ctx, env.Runner, name)
	if err != nil {
		return f, detect.Degradedf("%s; the host disk image only", err)
	}
	f.DF = rows
	f.Detail = systemDFVerbose(ctx, env.Runner, name)

	if len(degraded) > 0 {
		return f, detect.Degradedf("%s", strings.Join(degraded, "; "))
	}
	return f, nil
}

// listMachines runs `orb list`, preferring the JSON form and falling back to
// the table when an older orb does not understand -f.
func listMachines(ctx context.Context, r probe.Runner) ([]Machine, error) {
	res := r.Run(ctx, probe.Cmd{Name: "orb", Args: []string{"list", "-f", "json"}})
	if res.OK() {
		if machines, err := parseMachinesJSON(res.Stdout); err == nil {
			return machines, nil
		}
	}
	plain := r.Run(ctx, probe.Cmd{Name: "orb", Args: []string{"list"}})
	if !plain.OK() {
		return nil, fmt.Errorf("orb list: %s", plain.Reason())
	}
	return parseMachinesTable(plain.Stdout), nil
}

// rawMachine is one entry of `orb list -f json`. The image field is an object
// in current versions and was a string in older ones, so it is decoded
// loosely: a detector that failed because a nested field changed shape would
// lose the machine list over a cosmetic difference.
type rawMachine struct {
	Name  string          `json:"name"`
	State string          `json:"state"`
	Image json.RawMessage `json:"image"`
}

// parseMachinesJSON decodes `orb list -f json`. An empty array is the answer
// on a machine used only for Docker, which is this one.
func parseMachinesJSON(out string) ([]Machine, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, errors.New("orb list printed nothing")
	}
	var raw []rawMachine
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, err
	}
	machines := make([]Machine, 0, len(raw))
	for _, m := range raw {
		machines = append(machines, Machine{Name: m.Name, State: m.State, Image: imageName(m.Image)})
	}
	return machines, nil
}

// imageName renders the image field however it is shaped.
func imageName(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Distro  string `json:"distro"`
		Version string `json:"version"`
		Arch    string `json:"arch"`
	}
	if json.Unmarshal(raw, &obj) != nil || obj.Distro == "" {
		return ""
	}
	if obj.Version == "" {
		return obj.Distro
	}
	return obj.Distro + " " + obj.Version
}

// parseMachinesTable reads the plain `orb list` table: a header line and then
// one machine per line, name first and state second.
func parseMachinesTable(out string) []Machine {
	var machines []Machine
	for i, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if i == 0 && strings.EqualFold(fields[0], "NAME") {
			continue
		}
		m := Machine{Name: fields[0]}
		if len(fields) > 1 {
			m.State = strings.ToLower(fields[1])
		}
		if len(fields) > 3 {
			m.Image = fields[2] + " " + fields[3]
		}
		machines = append(machines, m)
	}
	return machines
}

// dockerContext is one line of `docker context ls --format json`.
type dockerContext struct {
	Name           string `json:"Name"`
	DockerEndpoint string `json:"DockerEndpoint"`
	Current        bool   `json:"Current"`
	Error          string `json:"Error"`
}

// socketMark is the part of an endpoint that proves a context is OrbStack's.
// Matching on the socket rather than only on the name is what stops a user
// who renamed their context from losing the runtime, and what stops the
// Docker Desktop detector from claiming this one.
const socketMark = ".orbstack/run/docker.sock"

// findContext picks the docker context that talks to OrbStack.
func findContext(ctx context.Context, r probe.Runner) (name, endpoint string, err error) {
	res := r.Run(ctx, probe.Cmd{Name: "docker", Args: []string{"context", "ls", "--format", "json"}})
	if !res.OK() {
		return "", "", fmt.Errorf("docker context ls: %s", res.Reason())
	}
	for _, c := range parseContexts(res.Stdout) {
		if c.Name == Name || strings.Contains(c.DockerEndpoint, socketMark) {
			return c.Name, c.DockerEndpoint, nil
		}
	}
	return "", "", errors.New("no docker context points at OrbStack")
}

// parseContexts reads the one-object-per-line form the docker CLI prints.
// Older versions print a single JSON array instead, so both are accepted.
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

// dfRow is one line of `docker system df --format json`. Every value is a
// human-formatted string even inside the JSON, which is why it is decoded
// into strings and parsed afterwards.
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
		return nil, fmt.Errorf("docker system df: %s", res.Reason())
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
			Type:        raw.Type,
			Size:        size,
			Reclaimable: reclaimable,
			Percent:     pct,
			PercentOK:   pctOK,
			Count:       probe.ParseCount(raw.TotalCount),
			Active:      probe.ParseCount(raw.Active),
		})
	}
	return rows
}

// systemDFVerbose is the optional per-image detail. The docker CLI has never
// supported -v together with --format, so it is run unformatted and kept as
// text; a failure costs nothing, so it is not reported.
func systemDFVerbose(ctx context.Context, r probe.Runner, dockerCtx string) string {
	res := r.Run(ctx, probe.Cmd{
		Name:    "docker",
		Args:    []string{"--context", dockerCtx, "system", "df", "-v"},
		Timeout: dfTimeout,
	})
	if !res.OK() {
		return ""
	}
	if len(res.Stdout) > maxDetail {
		return res.Stdout[:maxDetail]
	}
	return res.Stdout
}

// Classify turns the facts and the tree into claims and the containers view.
//
// The paths are claimed whether or not the probe succeeded: they are OrbStack's
// directories either way, and a degraded probe should cost the evidence rather
// than the bucket.
func (*Detector) Classify(t *walk.Tree, f detect.Facts, cx classify.Context) ([]classify.Claim, detect.Summary) {
	facts, _ := f.(*Facts)
	home := cx.Home
	if home == "" {
		return nil, detect.Summary{}
	}

	evidence := evidenceLines(facts)
	targets := []detect.Target{
		{
			Path: path.Join(home, group), Category: "OrbStack", Kind: "data",
			Reclaim: classify.ToolManaged,
			Explain: "OrbStack's disk image and swap; the image is sparse, so this is what the host has actually given it",
		},
		{
			Path: path.Join(home, ".orbstack"), Category: "OrbStack", Kind: "data",
			Reclaim: classify.Regenerable,
			Explain: "OrbStack's configuration, sockets and logs",
		},
		{
			Path: path.Join(home, "Library/Caches/dev.orbstack.OrbStack"), Category: "OrbStack", Kind: "cache",
			Reclaim: classify.Regenerable, Explain: "OrbStack's cache",
		},
		{
			Path: path.Join(home, "Library/Caches/dev.kdrag0n.MacVirt"), Category: "OrbStack", Kind: "cache",
			Reclaim: classify.Regenerable, Explain: "the OrbStack helper's cache",
		},
		{
			Path: path.Join(home, "Library/Application Support/OrbStack"), Category: "OrbStack", Kind: "data",
			Reclaim: classify.Unknown, Explain: "OrbStack's application data",
		},
	}
	for i := range targets {
		targets[i].Bucket = classify.BucketContainers
		targets[i].Owner = "OrbStack"
		targets[i].OwnerKeys = ownerKeys
		targets[i].Evidence = evidence
	}

	claims, tools := detect.Claims(t, Name, targets)
	if len(claims) == 0 {
		return nil, detect.Summary{}
	}
	return claims, detect.Summary{Tools: tools, Runtimes: []detect.Runtime{runtime(t, facts, home, evidence)}}
}

// imageFiles are the sparse files inside the group container that hold the
// whole of a guest's disk, listed with what to call them.
var imageFiles = []struct{ rel, name, note string }{
	{"data/data.img.raw", "data.img.raw", "the Linux disk image, sparse: allocated blocks, not its apparent size"},
	{"data/swap.img", "swap.img", "the guest's swap file"},
}

// runtime builds the two-column view: what the host has allocated, and what
// the daemon believes it is using.
func runtime(t *walk.Tree, f *Facts, home string, evidence []string) detect.Runtime {
	rt := detect.Runtime{Name: "OrbStack"}
	for _, img := range imageFiles {
		n, ok := detect.Lookup(t, path.Join(home, group, img.rel))
		if !ok {
			continue
		}
		note := img.note
		if n.Apparent > n.Bytes {
			note = fmt.Sprintf("%s apparent, %s allocated", units.Decimal.Bytes(n.Apparent), units.Decimal.Bytes(n.Bytes))
		}
		rt.HostImage = append(rt.HostImage, detect.Tool{
			Name: img.name, Kind: "image", Path: n.Display(), Node: n.ID,
			Bytes: n.Bytes, Reclaim: classify.ToolManaged, Note: note,
		})
	}
	if f == nil {
		rt.Note = "OrbStack did not answer; the host disk image is all that could be measured"
		return rt
	}

	rt.Context = f.Context
	for _, m := range f.Machines {
		label := m.Name
		if m.State != "" {
			label += " (" + m.State + ")"
		}
		rt.Machines = append(rt.Machines, label)
	}
	for _, row := range f.DF {
		rt.GuestReported = append(rt.GuestReported, detect.Line{
			Type: row.Type, Size: row.Size, Reclaimable: row.Reclaimable,
			Percent: row.Percent, Known: row.PercentOK,
			Count: row.Count, Active: row.Active,
			Reclaim: rowReclaim(row.Type),
		})
	}
	rt.Note = note(rt, f, evidence)
	return rt
}

// rowReclaim is how safely a df row's reclaimable share could be freed. A
// named volume is user data whatever the daemon calls reclaimable: it is
// where a database lives.
func rowReclaim(rowType string) classify.Reclaim {
	if strings.Contains(strings.ToLower(rowType), "volume") {
		return classify.UserData
	}
	return classify.ToolManaged
}

// note explains the gap between the two columns, which is the one thing a
// reader of this table always asks about.
func note(rt detect.Runtime, f *Facts, _ []string) string {
	switch {
	case len(f.DF) == 0 && f.Context == "":
		return "no Docker daemon answered, so only the host's side of the image is known"
	case len(f.DF) == 0:
		return "the daemon on context " + f.Context + " did not report its usage"
	}
	host, guest := rt.HostBytes(), rt.GuestBytes()
	u := units.Decimal
	switch {
	case host >= guest:
		return fmt.Sprintf("the host has given the image %s and the daemon accounts for %s of it; "+
			"the %s difference is slack OrbStack shrinks away on its own",
			u.Bytes(host), u.Bytes(guest), u.Bytes(host-guest))
	default:
		return fmt.Sprintf("the daemon accounts for %s inside an image the host has given %s; "+
			"the image is sparse and its contents compress, so the guest's figure is the larger one",
			u.Bytes(guest), u.Bytes(host))
	}
}

// evidenceLines are the why-panel lines every OrbStack claim carries.
func evidenceLines(f *Facts) []string {
	if f == nil {
		return []string{"orbstack did not answer; the paths come from the static catalog"}
	}
	var out []string
	if len(f.Machines) == 0 {
		out = append(out, "`orb list` reported no Linux machines")
	} else {
		names := make([]string, 0, len(f.Machines))
		for _, m := range f.Machines {
			names = append(names, m.Name)
		}
		out = append(out, "`orb list` reported "+strings.Join(names, ", "))
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
