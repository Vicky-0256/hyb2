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
		})
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
