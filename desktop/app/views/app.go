// Package views is the native interface of HYB2 Desktop: one project, one
// navigation (① Data ② QC ③ Explore ④ Compare ⑤ Structure ⑥ Report), a top
// bar holding the current selection, and a tasks drawer. It talks to the
// Python runtime only through core.Backend.
package views

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
	"hyb2desktop/model"
)

// Poster runs a function on the main thread and redraws (mygo.Window.Update).
type Poster interface {
	Update(fn func())
}

// Page is one entry of the navigation.
type Page interface {
	// View builds the page; it runs every frame and must not block.
	View(c *ui.Context)
	// Show runs when the page becomes visible or the project changes.
	Show()
}

// PageInfo describes a navigation entry.
type PageInfo struct {
	ID, Number, Title string
}

// Pages in navigation order.
var Pages = []PageInfo{
	{"data", "1", "Data"},
	{"qc", "2", "QC"},
	{"explore", "3", "Explore"},
	{"compare", "4", "Compare"},
	{"structure", "5", "Structure"},
	{"report", "6", "Report"},
}

// Recent is a remembered project.
type Recent struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	OpenedAt string `json:"opened_at"`
}

// App is the window's state.
type App struct {
	B   core.Backend
	win Poster
	// DataDir holds recent.json; empty disables persistence (tests).
	DataDir string

	Project *model.Project
	opened  int // counts setProject: pages tell one opening from the next
	opening int // counts Open calls: only the latest one's reply applies
	Tasks   map[string]model.Task
	labels  map[string]string

	Page     string
	pages    map[string]Page
	Selected Selection

	drawerOpen bool
	prefsOpen  bool
	prefs      prefsState
	// the frame (frame.go): theme preference, command palette, help
	theme                 string // "system", "light" or "dark"
	themeOnce             sync.Once
	lightTheme, darkTheme *ui.Theme
	palette               paletteState
	helpOpen              bool
	// motion is the Motion preference (motion.go): "" follows the
	// system, "full" or "reduced".
	motion string
	// systemReduce is the system's Reduce Motion as of the last frame.
	systemReduce bool
	// privacyOpen shows Privacy over the start page (a project shows it as
	// a page).
	privacyOpen bool
	// clipboard is text for the clipboard in the next frame (copyText).
	clipboard *string
	// autoKey is the selection normalizeSelection last chose by itself.
	autoKey string
	// chord is the "g" of a go-to chord, pressed at chordAt.
	chord   bool
	chordAt time.Time
	// Density is the Interactions table's rows: densityLabels[0] or [1].
	Density string
	lookup  lookupPref // external_lookup (evidence.go)
	start   startState
	// content is the page area of the previous frame (charts size to it).
	content ui.Rect
	// pendingFolder is a data folder chosen with a new project; the Data
	// page scans it once that project (and no other) is open.
	pendingFolder pendingFolder
	recent        []Recent

	toasts   []appToast
	uiSaves  uiSaver
	pollOnce sync.Once
	pollStop chan struct{}
	mu       sync.Mutex // guards pending (written from goroutines)
	pending  []func()
	// Navigate options for the next Show (e.g. a structure to display).
	NavOptions map[string]any
	// SessionStart separates this session's failures from older ones.
	SessionStart string
}

// Selection is the top bar's current choice.
type Selection struct {
	ReferenceID string
	Gene        string
	// Key is AllSelection, "c:<condition>" or "s:<sample id>".
	Key string
}

// New makes the app state; pages are registered by RegisterPage.
func New(b core.Backend, win Poster, dataDir string) *App {
	a := &App{B: b, win: win, DataDir: dataDir, Page: "data", theme: core.DefaultTheme, Tasks: map[string]model.Task{},
		labels: map[string]string{}, pages: map[string]Page{},
		SessionStart: time.Now().UTC().Format(time.RFC3339)}
	a.recent = a.loadRecent()
	a.motion = a.loadMotion()
	for _, p := range append(append([]PageInfo(nil), Pages...), HelpPages...) {
		if factory := pageFactories[p.ID]; factory != nil {
			a.pages[p.ID] = factory(a)
		} else {
			a.pages[p.ID] = &placeholder{a: a, info: p}
		}
	}
	return a
}

// pageFactories holds the pages that page files register in init().
var pageFactories = map[string]func(a *App) Page{}

// RegisterPageFactory is called from a page file's init(), so each page
// lives in its own file and no shared file lists them.
func RegisterPageFactory(id string, factory func(a *App) Page) { pageFactories[id] = factory }

// SetPoster attaches the window once it exists.
func (a *App) SetPoster(win Poster) { a.win = win }

// RegisterPage replaces a navigation entry's page.
func (a *App) RegisterPage(id string, p Page) { a.pages[id] = p }

// PageFor returns the page registered for id (for tests).
func (a *App) PageFor(id string) Page { return a.pages[id] }

// ---- async work

// Go runs work off the main thread and applies its result on the main
// thread: work returns the function that changes the state.
func (a *App) Go(work func() func()) {
	go func() {
		apply := work()
		if apply != nil {
			a.post(apply)
		}
	}()
}

func (a *App) post(fn func()) {
	if a.win != nil {
		a.win.Update(fn)
		return
	}
	a.mu.Lock()
	a.pending = append(a.pending, fn)
	a.mu.Unlock()
}

// Flush applies work posted without a window (tests).
func (a *App) Flush() {
	a.mu.Lock()
	pending := a.pending
	a.pending = nil
	a.mu.Unlock()
	for _, fn := range pending {
		fn()
	}
}

// appToast is a queued toast; one with the ID of a toast showing replaces it.
type appToast struct {
	ID, Text string
}

// Toast queues a message shown at the next frame.
func (a *App) Toast(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	a.ToastAs(text, "%s", text)
}

// ToastAs queues a message under id: it replaces the toast of the same id
// (queued or showing), as a newer outcome of the same action.
func (a *App) ToastAs(id, format string, args ...any) {
	kept := a.toasts[:0]
	for _, m := range a.toasts {
		if m.ID != id {
			kept = append(kept, m)
		}
	}
	a.toasts = append(kept, appToast{ID: id, Text: fmt.Sprintf(format, args...)})
}

// ---- project

// pendingFolder is a data folder to scan into the project at path.
type pendingFolder struct {
	project, folder string
}

// Open opens a project directory and shows it.
func (a *App) Open(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	if a.pendingFolder.project != path {
		a.pendingFolder = pendingFolder{} // chosen for a project this Open replaces
	}
	a.start.status = "Opening " + path + "…"
	a.opening++
	seq := a.opening
	a.Go(func() func() {
		opened, err := a.B.OpenProject(path)
		if err != nil {
			return func() {
				if seq != a.opening {
					return // a later Open replaced this one
				}
				a.pendingFolder = pendingFolder{}
				a.start.status = openError(err)
				a.Toast("Could not open the project: %v", err)
			}
		}
		project := projectFromOpen(path, opened)
		listed, listErr := a.B.ProjectOp(project.Path, "list_samples", nil)
		return func() {
			if seq != a.opening {
				return
			}
			a.start.status = ""
			if listErr == nil {
				applySamples(project, listed)
			}
			if a.pendingFolder.project == path {
				a.pendingFolder.project = project.Path // as the project reports it
			} else {
				a.pendingFolder = pendingFolder{}
			}
			a.setProject(project)
		}
	})
}

func projectFromOpen(path string, opened core.Obj) *model.Project {
	p := &model.Project{Path: path}
	if s, _ := opened["path"].(string); s != "" {
		p.Path = s
	}
	manifest, _ := opened["manifest"].(map[string]any)
	p.Name, _ = manifest["name"].(string)
	p.Description, _ = manifest["description"].(string)
	_ = model.Decode(manifest["references"], &p.References)
	_ = model.Decode(manifest["samples"], &p.Samples)
	p.UIState, _ = manifest["ui_state"].(map[string]any)
	if p.Name == "" {
		p.Name = filepath.Base(p.Path)
	}
	p.Conditions = model.DeriveConditions(p.Samples)
	return p
}

func applySamples(p *model.Project, listed core.Obj) {
	var samples []model.Sample
	if model.Decode(listed["samples"], &samples) == nil {
		p.Samples = samples
	}
	var refs []model.Reference
	if model.Decode(listed["references"], &refs) == nil && refs != nil {
		p.References = refs
	}
	var untrusted []model.UntrustedLink
	if model.Decode(listed["untrusted_links"], &untrusted) == nil {
		p.UntrustedLinks = untrusted
	}
	var conditions []model.Condition
	if model.Decode(listed["conditions"], &conditions) == nil && len(conditions) > 0 {
		p.Conditions = conditions
	} else {
		p.Conditions = model.DeriveConditions(p.Samples)
	}
}

func (a *App) setProject(p *model.Project) {
	a.Project = p
	a.opened++
	a.Tasks = map[string]model.Task{}
	a.autoKey = ""
	a.restoreUIState()
	a.normalizeSelection()
	a.remember(p.Path, p.Name)
	a.RefreshTasks()
	a.showPage()
}

// Close returns to the start page; the next project opens on Data unless
// its saved state names another page.
func (a *App) Close() {
	a.opened++ // a reply for the closed project is not this one's
	a.Project = nil
	a.Tasks = map[string]model.Task{}
	a.pendingFolder = pendingFolder{}
	a.drawerOpen = false
	a.Page = "data"
	a.NavOptions = nil
	a.chord = false
}

// RefreshProject reloads samples, references and conditions.
func (a *App) RefreshProject() {
	if a.Project == nil {
		return
	}
	path, gen := a.Project.Path, a.opened
	a.Go(func() func() {
		listed, err := a.B.ProjectOp(path, "list_samples", nil)
		return func() {
			if !a.current(path, gen) {
				return
			}
			if err != nil {
				a.Toast("Could not refresh the project: %v", err)
				return
			}
			applySamples(a.Project, listed)
			before := a.Selected
			a.normalizeSelection()
			if a.Selected != before {
				a.saveUIState() // a removed sample or reference changed it
			}
		}
	})
}

// ---- selection

// SelectionOption is one entry of the selection pickers.
type SelectionOption struct {
	Key, Label, Group string
}

// AllSelection is the selection key of every analysed sample merged (the
// web's "All N datasets merged" scope).
const AllSelection = "all"

// SelectionOptions: every analysed sample merged (two or more), the
// conditions merging some of them, then analysed samples. As the web's
// scopes (app.js analysisScopes), a condition of one sample is left to that
// sample and one holding every sample to "All".
func (a *App) SelectionOptions() []SelectionOption {
	return a.selectionOptions(false)
}

// selectionOptions are SelectionOptions; groups (Compare's) keeps every
// condition with an analysed sample and leaves out AllSelection.
func (a *App) selectionOptions(groups bool) []SelectionOption {
	if a.Project == nil {
		return nil
	}
	var out []SelectionOption
	all := len(a.SelectionIDs(AllSelection))
	if all > 1 && !groups {
		out = append(out, SelectionOption{Key: AllSelection, Label: a.SelectionLabel(AllSelection), Group: "All"})
	}
	for _, cond := range a.Project.Conditions {
		key := "c:" + cond.Name
		n := len(a.SelectionIDs(key))
		if n == 0 || !groups && (n == 1 || all > 1 && n == all) {
			continue
		}
		out = append(out, SelectionOption{Key: key, Label: a.SelectionLabel(key), Group: "Conditions"})
	}
	for _, s := range a.Project.Samples {
		if !s.IsReady() {
			continue
		}
		label := s.Label()
		if s.Condition != "" {
			label += " · " + s.Condition
		}
		out = append(out, SelectionOption{Key: "s:" + s.ID, Label: label, Group: "Samples"})
	}
	return out
}

// GroupOptions are the groups two of which Compare contrasts: every
// condition with an analysed sample, then the samples; not AllSelection.
func (a *App) GroupOptions() []SelectionOption {
	return a.selectionOptions(true)
}

// SelectionLabels are the options' labels for a picker that tells them
// apart by label: a label already taken gets " (2)", " (3)"… so that two
// samples labelled alike both stay choosable.
func SelectionLabels(options []SelectionOption) []string {
	out := make([]string, len(options))
	own := map[string]bool{} // the labels as given, which a suffix avoids
	for _, o := range options {
		own[o.Label] = true
	}
	taken := map[string]bool{}
	for i, o := range options {
		label := o.Label
		for n := 2; taken[label]; n++ {
			if label = fmt.Sprintf("%s (%d)", o.Label, n); own[label] {
				label = o.Label // " (n)" is another option's own label
			}
		}
		taken[label] = true
		out[i] = label
	}
	return out
}

// SelectionIDs are the analysed sample ids behind a selection key.
func (a *App) SelectionIDs(key string) []string {
	if a.Project == nil {
		return nil
	}
	var ids []string
	switch {
	case key == AllSelection:
		for _, s := range a.Project.Samples {
			if s.IsReady() {
				ids = append(ids, s.ID)
			}
		}
	case strings.HasPrefix(key, "c:"):
		name := key[2:]
		for _, cond := range a.Project.Conditions {
			if cond.Name != name {
				continue
			}
			for _, id := range cond.SampleIDs {
				if s := a.Project.Sample(id); s != nil && s.IsReady() {
					ids = append(ids, id)
				}
			}
		}
	case strings.HasPrefix(key, "s:"):
		if s := a.Project.Sample(key[2:]); s != nil && s.IsReady() {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// SelectionLabel names a selection key, in the options' (and the web's)
// words.
func (a *App) SelectionLabel(key string) string {
	switch {
	case key == AllSelection:
		return fmt.Sprintf("All %d datasets merged", len(a.SelectionIDs(key)))
	case strings.HasPrefix(key, "c:"):
		n := len(a.SelectionIDs(key))
		if n == 1 {
			return key[2:] + " · 1 dataset"
		}
		return fmt.Sprintf("%s · %d datasets merged", key[2:], n)
	case strings.HasPrefix(key, "s:"):
		if a.Project != nil {
			if s := a.Project.Sample(key[2:]); s != nil {
				return s.Label()
			}
		}
		return key[2:]
	}
	return "nothing selected"
}

// Genes of the selected reference.
func (a *App) Genes() []model.Sequence {
	if a.Project == nil {
		return nil
	}
	if r := a.Project.Reference(a.Selected.ReferenceID); r != nil {
		return r.Sequences
	}
	return nil
}

func (a *App) normalizeSelection() {
	p := a.Project
	if p == nil {
		return
	}
	if p.Reference(a.Selected.ReferenceID) == nil {
		a.Selected.ReferenceID = ""
		if len(p.References) > 0 {
			a.Selected.ReferenceID = p.References[0].ID
		}
	}
	genes := a.Genes()
	found := false
	for _, g := range genes {
		found = found || g.Name == a.Selected.Gene
	}
	if !found {
		a.Selected.Gene = ""
		if len(genes) > 0 {
			a.Selected.Gene = genes[0].Name
		}
	}
	options := a.SelectionOptions()
	found = false
	for _, o := range options {
		found = found || o.Key == a.Selected.Key
	}
	// every analysed sample merged when there are several, as the web
	// analyses all loaded datasets until a scope is chosen (app.js
	// analyseAllLoaded); else the one sample. A key chosen here gives way to
	// "All" once a second sample is analysed.
	auto := a.Selected.Key != "" && a.Selected.Key == a.autoKey
	if !found || auto && len(options) > 0 && options[0].Key == AllSelection {
		a.Selected.Key = ""
		if len(options) > 0 {
			a.Selected.Key = options[0].Key
		}
		a.autoKey = a.Selected.Key
	}
}

// ---- navigation and ui_state

// Navigate shows a page; options reach its Show through NavOptions.
func (a *App) Navigate(page string, options map[string]any) {
	if _, ok := a.pages[page]; !ok {
		return
	}
	a.Page = page
	a.NavOptions = options
	a.showPage()
	a.saveUIState()
}

func (a *App) showPage() {
	if p := a.pages[a.Page]; p != nil && a.Project != nil {
		p.Show()
	}
}

func (a *App) restoreUIState() {
	desktop, _ := a.Project.UIState["desktop"].(map[string]any)
	if page, _ := desktop["page"].(string); page != "" {
		if _, ok := a.pages[page]; ok {
			a.Page = page
		}
	}
	a.Selected.ReferenceID, _ = desktop["reference_id"].(string)
	a.Selected.Gene, _ = desktop["gene"].(string)
	a.Selected.Key, _ = desktop["selection"].(string)
	a.Density, _ = desktop["density"].(string)
}

func (a *App) saveUIState() {
	if a.Project == nil {
		return
	}
	path := a.Project.Path
	state := map[string]any{}
	for k, v := range a.Project.UIState {
		state[k] = v
	}
	desktop := map[string]any{"page": a.Page, "reference_id": a.Selected.ReferenceID, "gene": a.Selected.Gene, "selection": a.Selected.Key,
		"density": a.Density}
	state["desktop"] = desktop
	a.Project.UIState = state
	a.uiSaves.save(a, path, state)
}

// uiSaver writes ui_state one save at a time, in order; a save asked for
// while one is written replaces any still waiting, so the latest is the
// last written.
type uiSaver struct {
	mu      sync.Mutex
	writing bool
	next    *uiSave
}

type uiSave struct {
	path  string
	state map[string]any
}

func (s *uiSaver) save(a *App, path string, state map[string]any) {
	s.mu.Lock()
	s.next = &uiSave{path, state}
	if s.writing {
		s.mu.Unlock()
		return
	}
	s.writing = true
	s.mu.Unlock()
	go func() {
		for {
			s.mu.Lock()
			job := s.next
			s.next = nil
			if job == nil {
				s.writing = false
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			if _, err := a.B.UpdateUIState(job.path, job.state); err != nil {
				a.post(func() { a.ToastAs("ui-state", "Could not save the page and selection of the project: %v", err) })
			}
		}
	}()
}

// ---- recent projects

func (a *App) loadRecent() []Recent {
	if a.DataDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(a.DataDir, "recent.json"))
	if err != nil {
		return nil
	}
	var out []Recent
	_ = json.Unmarshal(raw, &out)
	return out
}

func (a *App) remember(path, name string) {
	list := []Recent{{Path: path, Name: name, OpenedAt: time.Now().UTC().Format(time.RFC3339)}}
	for _, r := range a.recent {
		if r.Path != path && len(list) < 8 {
			list = append(list, r)
		}
	}
	a.recent = list
	a.saveRecent()
}

func (a *App) forget(path string) {
	var list []Recent
	for _, r := range a.recent {
		if r.Path != path {
			list = append(list, r)
		}
	}
	a.recent = list
	a.saveRecent()
}

func (a *App) saveRecent() {
	if a.DataDir == "" {
		return
	}
	raw, _ := json.MarshalIndent(a.recent, "", "  ")
	_ = os.MkdirAll(a.DataDir, 0o755)
	_ = core.WriteFileAtomic(filepath.Join(a.DataDir, "recent.json"), raw)
}

// ---- tasks

var kindLabels = map[string]string{"sample_pipeline": "Pipeline", "analysis_index": "Index", "structure": "Structure",
	"compare": "Differential test", "energy": "Folding energy", "batch_energy": "Binding energies", "cplfold": "CPLfold",
	"validate_project": "Validation", "pipeline": "Pipeline"}

// TaskLabel names a task in lists and toasts.
func (a *App) TaskLabel(t model.Task) string {
	if l := a.labels[t.TaskID]; l != "" {
		return l
	}
	if l := kindLabels[t.Kind]; l != "" {
		return l + " · " + t.TaskID
	}
	return t.TaskID
}

// SetTaskLabel gives a task a readable name.
func (a *App) SetTaskLabel(id, label string) { a.labels[id] = label }

// ActiveTasks: queued and running, newest first.
func (a *App) ActiveTasks() []model.Task {
	var out []model.Task
	for _, t := range a.Tasks {
		if t.Active() {
			out = append(out, t)
		}
	}
	model.SortTasks(out)
	return out
}

// TaskList: all known tasks, newest first.
func (a *App) TaskList() []model.Task {
	out := make([]model.Task, 0, len(a.Tasks))
	for _, t := range a.Tasks {
		out = append(out, t)
	}
	model.SortTasks(out)
	return out
}

// errProjectChanged ends a callback whose project is no longer open.
var errProjectChanged = errors.New("the project was closed or another one opened")

// current reports whether the project opened as generation gen at path is
// still the open one: replies for an earlier project are dropped.
func (a *App) current(path string, gen int) bool {
	return a.Project != nil && a.Project.Path == path && a.opened == gen
}

// StartTask starts a task; done runs on the main thread with the snapshot.
func (a *App) StartTask(req core.TaskStartRequest, label string, done func(model.Task, error)) {
	if a.Project == nil {
		return
	}
	path, gen := a.Project.Path, a.opened
	req.ProjectPath = path
	a.Go(func() func() {
		snapshot, err := a.B.StartTask(req)
		var t model.Task
		if err == nil {
			err = model.Decode(snapshot, &t)
		}
		return func() {
			if !a.current(path, gen) {
				// it runs in its own project, which lists it when reopened
				if done != nil {
					done(t, errProjectChanged)
				}
				return
			}
			if err == nil {
				if label != "" {
					a.labels[t.TaskID] = label
				}
				a.storeTask(t)
				a.ensurePolling()
			}
			if done != nil {
				done(t, err)
			}
		}
	})
}

// StartTasksInOrder submits tasks one after another (so the scheduler's
// queue follows the given order) and calls done(i, task, err) for each on
// the main thread.
func (a *App) StartTasksInOrder(reqs []core.TaskStartRequest, labels []string, done func(i int, t model.Task, err error)) {
	if a.Project == nil || len(reqs) == 0 {
		return
	}
	path, gen := a.Project.Path, a.opened
	for i := range reqs {
		reqs[i].ProjectPath = path
	}
	go func() {
		for i, req := range reqs {
			snapshot, err := a.B.StartTask(req)
			var t model.Task
			if err == nil {
				err = model.Decode(snapshot, &t)
			}
			i, label := i, ""
			if i < len(labels) {
				label = labels[i]
			}
			a.post(func() {
				if !a.current(path, gen) {
					if done != nil {
						done(i, t, errProjectChanged)
					}
					return
				}
				if err == nil {
					if label != "" {
						a.labels[t.TaskID] = label
					}
					a.storeTask(t)
					a.ensurePolling()
				}
				if done != nil {
					done(i, t, err)
				}
			})
		}
	}()
}

// CancelTask asks the scheduler to cancel a task.
func (a *App) CancelTask(id string) {
	if a.Project == nil {
		return
	}
	path, gen := a.Project.Path, a.opened
	a.Go(func() func() {
		snapshot, err := a.B.CancelTask(path, id)
		return func() {
			if !a.current(path, gen) {
				return
			}
			if err != nil {
				a.Toast("Could not cancel: %v", err)
				return
			}
			var t model.Task
			if model.Decode(snapshot, &t) == nil {
				a.storeTask(t)
			}
		}
	})
}

// ResumeTask restarts an interrupted or failed task.
func (a *App) ResumeTask(id string) { a.ResumeTaskThen(id, "", nil) }

// ResumeTaskThen resumes a task under a new id (labelled like the old one,
// or label); done runs on the main thread with the new snapshot.
func (a *App) ResumeTaskThen(id, label string, done func(model.Task, error)) {
	if a.Project == nil {
		return
	}
	path, gen := a.Project.Path, a.opened
	a.Go(func() func() {
		snapshot, err := a.B.ResumeTask(path, id)
		var t model.Task
		if err == nil {
			err = model.Decode(snapshot, &t)
		}
		return func() {
			if !a.current(path, gen) {
				if done != nil {
					done(t, errProjectChanged)
				}
				return
			}
			if err == nil {
				if label == "" {
					label = a.labels[id]
				}
				if label != "" {
					a.labels[t.TaskID] = label
				}
				a.storeTask(t)
				a.ensurePolling()
			}
			if done != nil {
				done(t, err)
			} else if err != nil {
				a.Toast("Could not resume: %v", err)
			}
		}
	})
}

// RefreshTasks loads the project's task list.
func (a *App) RefreshTasks() {
	if a.Project == nil {
		return
	}
	path, gen := a.Project.Path, a.opened
	a.Go(func() func() {
		list, err := a.B.ListTasks(path)
		return func() {
			if err != nil || !a.current(path, gen) {
				return
			}
			for _, item := range list {
				var t model.Task
				if model.Decode(item, &t) == nil {
					a.storeTask(t)
				}
			}
			if a.staleStages() {
				// Listing marked a task of an exited session interrupted
				// after the samples were read: read them again.
				a.RefreshProject()
			}
			a.ensurePolling()
		}
	})
}

// staleStages: a sample stage is shown queued or running for a task known
// to have finished.
func (a *App) staleStages() bool {
	if a.Project == nil {
		return false
	}
	for _, s := range a.Project.Samples {
		for _, stage := range s.Stages {
			if stage.Status != "running" && stage.Status != "queued" {
				continue
			}
			if t, ok := a.Tasks[stage.TaskID]; ok && !t.Active() {
				return true
			}
		}
	}
	return false
}

// TaskFinished listeners run on the main thread when a task leaves the
// queued/running states.
var taskFinishedHooks []func(a *App, t model.Task)

// OnTaskFinished registers a hook (pages register theirs at init).
func OnTaskFinished(fn func(a *App, t model.Task)) { taskFinishedHooks = append(taskFinishedHooks, fn) }

// taskStage orders a task's states: one only moves forward, so a snapshot
// read before a later one (a poll racing a cancel) is older when its stage
// is lower.
func taskStage(status string) int {
	switch status {
	case "queued":
		return 0
	case "running":
		return 1
	case "cancelling":
		return 2
	}
	return 3 // finished
}

// storeTask records a snapshot unless it is older than the one held: a
// finished task never returns to running, nor a cancelling one, so the
// finish hooks run once.
func (a *App) storeTask(t model.Task) {
	previous, known := a.Tasks[t.TaskID]
	if known && taskStage(t.Status) < taskStage(previous.Status) {
		return
	}
	a.Tasks[t.TaskID] = t
	if known && previous.Active() && !t.Active() {
		id := "task:" + t.TaskID
		switch t.Status {
		case "succeeded":
			a.ToastAs(id, "%s finished", a.TaskLabel(t))
		case "cancelled":
			a.ToastAs(id, "%s cancelled", a.TaskLabel(t))
		default:
			if reason := ShortError(t.ErrorText()); reason != "" {
				a.ToastAs(id, "%s %s: %s", a.TaskLabel(t), t.Status, reason)
			} else {
				a.ToastAs(id, "%s %s", a.TaskLabel(t), t.Status)
			}
		}
		a.RefreshProject()
		for _, hook := range taskFinishedHooks {
			hook(a, t)
		}
	}
}

// ensurePolling starts one poller that refreshes active tasks every 1.5 s.
func (a *App) ensurePolling() {
	if a.win == nil {
		return
	}
	a.pollOnce.Do(func() {
		stop := make(chan struct{})
		a.pollStop = stop
		go func() {
			ticker := time.NewTicker(1500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
				}
				// the main thread answers; a closed window never does
				type active struct {
					path string
					gen  int
					ids  []string
				}
				done := make(chan active, 1)
				a.post(func() {
					var got active
					if a.Project != nil {
						got.path, got.gen = a.Project.Path, a.opened
						for _, t := range a.ActiveTasks() {
							got.ids = append(got.ids, t.TaskID)
						}
					}
					done <- got
				})
				var got active
				select {
				case <-stop:
					return
				case got = <-done:
				}
				if len(got.ids) == 0 {
					continue
				}
				a.pollTasks(got.path, got.gen, got.ids)
			}
		}()
	})
}

// pollTasks reads the status of tasks ids of the project opened as
// generation gen at path.
func (a *App) pollTasks(path string, gen int, ids []string) {
	var snapshots []model.Task
	for _, id := range ids {
		snapshot, err := a.B.TaskStatus(path, id)
		if err != nil {
			continue
		}
		var t model.Task
		if model.Decode(snapshot, &t) == nil {
			snapshots = append(snapshots, t)
		}
	}
	a.post(func() {
		if !a.current(path, gen) {
			return
		}
		for _, t := range snapshots {
			a.storeTask(t)
		}
	})
}

// Stop ends background polling (window closed).
func (a *App) Stop() {
	if a.pollStop != nil {
		select {
		case <-a.pollStop:
		default:
			close(a.pollStop)
		}
	}
}

// sortedKeys is a small helper for deterministic map iteration.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ContentSize is the page area's width and height (the previous frame's;
// a typical window before the first one).
func (a *App) ContentSize() (w, h float32) {
	if a.content.W <= 0 || a.content.H <= 0 {
		return 916, 676
	}
	return a.content.W, a.content.H
}

// clampF limits v to [lo, hi].
func clampF(v, lo, hi float32) float32 { return max(lo, min(v, hi)) }
