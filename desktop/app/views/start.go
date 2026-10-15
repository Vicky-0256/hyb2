package views

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
)

type startState struct {
	openPath   string
	newPath    string
	newName    string
	status     string
	creating   bool
	runtimeErr string
	runtime    string
}

// Dialogs are native and block, so they run on a goroutine; tests replace them.
var (
	PickFolder = func(title string) string {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: title, Directory: true})
		if err != nil || len(paths) == 0 {
			return ""
		}
		return paths[0]
	}
	PickFiles = func(title string, multiple bool) []string {
		paths, _ := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: title, Multiple: multiple})
		return paths
	}
	PickSave = func(title, defaultPath string) string {
		path, _ := mygo.Dialog.Save(mygo.SaveDialogOptions{DefaultPath: defaultPath})
		return path
	}
)

// SetRuntimeStatus shows the bundled runtime's state on the start page.
func (a *App) SetRuntimeStatus(summary, err string) {
	a.start.runtime, a.start.runtimeErr = summary, err
}

func (a *App) startView(c *ui.Context) {
	t := c.Theme()
	s := &a.start
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).Padding(32).Gap(16).MaxWidth(960).Children(func() {
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "HYB2 Desktop").FontSize(26).Bold()
				ui.Text(c, "From raw chimeric reads to RNA structure, in one local project.").TextColor(t.TextMuted)
			})
			if s.runtimeErr != "" {
				ui.Text(c, "Runtime unavailable: "+s.runtimeErr).TextColor(t.Danger)
			}
			ui.Row(c).Gap(16).AlignItems(ui.Stretch).Wrap().Children(func() {
				card(c, "Open a project", func() {
					Field(c, "Project folder", func() {
						ui.TextInput(c.Key("open-path"), &s.openPath).Placeholder("/path/to/study.hyb2proj").Label("Project folder")
					})
					ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
						if ui.Button(c, "Browse…").Clicked() {
							a.Go(func() func() {
								path := PickFolder("Open a HYB2 project")
								return func() {
									if path != "" {
										s.openPath = path
										a.Open(path)
									}
								}
							})
						}
						if ui.PrimaryButton(c, "Open").Disabled(strings.TrimSpace(s.openPath) == "").Clicked() {
							a.Open(s.openPath)
						}
					})
				}).Grow(1).Basis(320)
				card(c, "New project", func() {
					Field(c, "Empty folder", func() {
						ui.TextInput(c.Key("new-path"), &s.newPath).Placeholder("/path/to/new-study.hyb2proj").Label("Empty folder")
					})
					Field(c, "Name", func() {
						ui.TextInput(c.Key("new-name"), &s.newName).Placeholder("ZIKV virion RNA").Label("Name")
					})
					ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
						if ui.Button(c, "Browse…").Clicked() {
							a.Go(func() func() {
								path := PickFolder("Choose an empty folder for the project")
								return func() {
									if path != "" {
										s.newPath = path
									}
								}
							})
						}
						ready := strings.TrimSpace(s.newPath) != "" && strings.TrimSpace(s.newName) != "" && !s.creating
						if ui.PrimaryButton(c, "Create and open").Disabled(!ready).Clicked() {
							a.create(strings.TrimSpace(s.newPath), strings.TrimSpace(s.newName))
						}
					})
				}).Grow(1).Basis(320)
			})
			if s.status != "" {
				ui.Text(c, s.status).TextColor(t.TextMuted)
			}
			if len(a.recent) > 0 {
				card(c, "Recent projects", func() {
					for _, r := range a.recent {
						r := r
						ui.Row(c.Key(r.Path)).Gap(8).Children(func() {
							ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Children(func() {
								if ui.Link(c, nameOr(r.Name, filepath.Base(r.Path)), "").Clicked() {
									a.Open(r.Path)
								}
								ui.Text(c, r.Path).FontSize(12).TextColor(t.TextMuted).SingleLine()
							})
							if ui.Button(c, "Forget").Clicked() {
								a.forget(r.Path)
							}
						})
					}
				})
			}
			if s.runtime != "" {
				ui.Text(c, s.runtime).FontSize(12).TextColor(t.TextMuted)
			}
		})
	})
}

func (a *App) create(path, name string) {
	a.start.creating = true
	a.start.status = "Creating " + path + "…"
	a.Go(func() func() {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			if entries, _ := os.ReadDir(path); len(entries) > 0 {
				return func() {
					a.start.creating = false
					a.start.status = "The folder is not empty; choose an empty or new folder."
				}
			}
		}
		_, err := a.B.CreateProject(path, name, "")
		return func() {
			a.start.creating = false
			if err != nil {
				a.start.status = fmt.Sprintf("Could not create the project: %v", err)
				return
			}
			a.start.newPath, a.start.newName = "", ""
			a.Open(path)
		}
	})
}

// card is a titled panel.
func card(c *ui.Context, title string, body func()) ui.Element {
	t := c.Theme()
	return ui.Column(c).Padding(16).Gap(10).Radius(10).Background(t.Background).Border(1, t.Border).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(16).Bold()
		}
		body()
	})
}

// Card is the shared panel for pages.
func Card(c *ui.Context, title string, body func()) ui.Element { return card(c, title, body) }

func nameOr(name, fallback string) string {
	if name != "" {
		return name
	}
	return fallback
}

// ---- preferences

type prefsState struct {
	loading bool
	err     string
	cpu     float64
	memMB   float64
	sysCPU  float64
	sysMem  float64
	custom  bool
	// External public-data lookups (EVIDENCE.md).
	lookup       bool
	lookupNotice bool // the first-enable notice is open
	testing      bool
	testMsg      string
	testErr      bool
}

func (a *App) openPrefs() {
	a.prefsOpen = true
	a.prefs = prefsState{loading: true}
	a.Go(func() func() {
		got, err := a.B.Preferences()
		return func() { a.applyPrefs(got, err) }
	})
}

func (a *App) applyPrefs(got core.Obj, err error) {
	a.prefs.loading = false
	if err != nil {
		a.prefs.err = err.Error()
		return
	}
	budget, _ := got["budget"].(map[string]any)
	system, _ := got["system"].(map[string]any)
	a.prefs.cpu, _ = budget["cpu"].(float64)
	a.prefs.memMB, _ = budget["mem_mb"].(float64)
	a.prefs.sysCPU, _ = system["cpus"].(float64)
	a.prefs.sysMem, _ = system["mem_mb"].(float64)
	a.prefs.custom, _ = got["custom_budget"].(bool)
	a.prefs.lookup, _ = got["external_lookup"].(bool)
	a.setLookup(got)
}

func (a *App) prefsDialog(c *ui.Context) {
	t := c.Theme()
	p := &a.prefs
	ui.Modal(c, &a.prefsOpen, func() {
		ui.Column(c).Width(420).Gap(12).Children(func() {
			ui.Text(c, "Preferences").FontSize(18).Bold()
			switch {
			case p.loading:
				ui.Text(c, "Loading…").TextColor(t.TextMuted)
				return
			case p.err != "":
				ui.Text(c, p.err).TextColor(t.Danger)
				return
			}
			ui.Text(c, "Resource budget for tasks").Bold()
			ui.Text(c, fmt.Sprintf("This computer: %.0f CPUs, %s MB memory. Tasks that do not fit wait in the queue.",
				p.sysCPU, FormatNumber(int64(p.sysMem)))).FontSize(12).TextColor(t.TextMuted)
			maxCPU := p.sysCPU
			if maxCPU < 1 {
				maxCPU = 1
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "CPUs").Width(120)
				ui.NumberInput(c.Key("cpu"), &p.cpu, 1, maxCPU, 1).Width(120)
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "Memory (MB)").Width(120)
				ui.NumberInput(c.Key("mem"), &p.memMB, 512, maxFloat(p.sysMem, 512), 256).Width(160)
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Use defaults").Disabled(!p.custom).Clicked() {
					a.savePrefs(core.Obj{"budget": nil})
				}
				if ui.PrimaryButton(c, "Save").Clicked() {
					a.savePrefs(core.Obj{"budget": core.Obj{"cpu": int(p.cpu), "mem_mb": int(p.memMB)}})
				}
			})
			ui.Divider(c)
			a.lookupPrefs(c)
		})
	})
}

// lookupPrefs is the external public-data lookup switch, its first-enable
// notice, the connection test and the cache.
func (a *App) lookupPrefs(c *ui.Context) {
	t := c.Theme()
	p := &a.prefs
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		on := p.lookup
		ui.Switch(c.Key("pref-lookup"), &on).Label("Allow external public-data lookups (RNAcentral)").OnClick(func() {
			switch {
			case on && !p.lookup:
				p.lookupNotice = true // enabled only after the notice
			case !on && p.lookup:
				p.lookupNotice = false
				a.savePrefs(core.Obj{"external_lookup": false})
				p.lookup = false
			}
		})
		ui.Text(c, "Allow external public-data lookups (RNAcentral)").Bold().Grow(1).Shrink(1).MinWidth(0)
	})
	ui.Text(c, "Off by default. Used by Structure › Related research.").FontSize(12).TextColor(t.TextMuted)
	if p.lookupNotice {
		ui.Column(c.Key("pref-lookup-notice")).Gap(8).Padding(10).Radius(8).Background(t.Warning.Alpha(0.12)).Children(func() {
			ui.Text(c, "Before you turn this on").Bold()
			ui.Text(c, "When you look up related research, HYB2 sends the public identifier and species you type — nothing else — "+
				"to RNAcentral (EMBL-EBI) over the internet. No sequence, structure, read file or project data is sent.").FontSize(12)
			ui.Text(c, "Even public identifiers can reveal what you are working on. Summaries are AI-generated background, "+
				"not a validation of your results.").FontSize(12)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Not now").Clicked() {
					p.lookupNotice = false
				}
				if ui.PrimaryButton(c, "Turn on").Clicked() {
					p.lookupNotice = false
					p.lookup = true
					a.savePrefs(core.Obj{"external_lookup": true})
				}
			})
		})
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, "Test connection").Disabled(!p.lookup || p.testing).Tooltip("Start the RNAcentral connection and list its tools").Clicked() {
			a.testLookup()
		}
		if ui.Button(c, "Clear cache").Tooltip("Forget the RNAcentral answers kept on this computer").Clicked() {
			a.Go(func() func() {
				err := a.B.ClearEvidenceCache()
				return func() {
					if err != nil {
						a.Toast("Could not clear the cache: %v", err)
						return
					}
					a.Toast("RNAcentral cache cleared")
				}
			})
		}
		if p.testing {
			ui.Spinner(c).Label("Testing")
		}
	})
	if p.testMsg != "" {
		color := t.TextMuted
		if p.testErr {
			color = t.Danger
		}
		ui.Text(c, p.testMsg).FontSize(12).TextColor(color)
	}
}

func (a *App) testLookup() {
	p := &a.prefs
	p.testing, p.testMsg, p.testErr = true, "", false
	a.Go(func() func() {
		status, err := a.B.EvidenceStatus(true)
		return func() {
			p.testing = false
			if err != nil {
				p.testMsg, p.testErr = "Connection failed: "+err.Error(), true
				return
			}
			state := "Worker ready"
			if status.Connected {
				state = "Connected"
			}
			p.testMsg = fmt.Sprintf("%s · RNAcentral MCP server %s · MCP SDK %s · %s · tools: %s", state,
				nameOr(status.ServerPackageVersion, "unknown"), nameOr(status.SDKVersion, "unknown"),
				nameOr(status.AdapterVersion, "unknown adapter"), nameOr(strings.Join(status.Tools, ", "), "none"))
		}
	})
}

func (a *App) savePrefs(patch core.Obj) {
	a.Go(func() func() {
		got, err := a.B.SetPreferences(patch)
		return func() {
			if err != nil {
				a.Toast("Could not save preferences: %v", err)
				return
			}
			a.applyPrefs(got, nil)
			a.Toast("Preferences saved")
		}
	})
}

func maxFloat(x, y float64) float64 {
	if x > y {
		return x
	}
	return y
}
