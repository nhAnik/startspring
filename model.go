package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

type state int

const (
	stateForm state = iota
	stateSpinner
	stateDone
)

type errMsg struct{ err error }

// model contains the program's state and implements
// tea.Model.
type model struct {
	state      state
	client     *http.Client
	info       *projectInfo
	data       *metadata
	finalMsg   string
	isQuitting bool

	form    *huh.Form
	spinner spinner.Model
}

func newModel(data *metadata, client *http.Client) model {
	info := &projectInfo{}
	return model{
		state:   stateForm,
		client:  client,
		info:    info,
		data:    data,
		form:    newForm(info, data),
		spinner: newSpinner(),
	}
}

func (m model) Init() tea.Cmd {
	return m.form.Init()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.String() == "ctrl+c" {
			m.isQuitting = true
			return m, tea.Quit
		}
	}

	switch m.state {
	case stateForm:

		form, cmd := m.form.Update(msg)
		if f, ok := form.(*huh.Form); ok {
			m.form = f
		}

		// After the form is completed, start the spinner and
		// generate the project.
		if m.form.State == huh.StateCompleted {
			m.state = stateSpinner
			return m, tea.Batch(m.spinner.Tick, m.generateProject())
		}
		return m, cmd

	case stateSpinner:

		if msg, ok := msg.(errMsg); ok {
			if msg.err == nil {
				m.finalMsg = "Project generated successfully!"
			} else {
				m.finalMsg = msg.err.Error()
			}
			m.state = stateDone
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	default:
		return m, tea.Quit
	}
}

func (m model) View() string {
	if m.isQuitting {
		return ""
	}
	switch m.state {
	case stateForm:
		return m.form.View()
	case stateSpinner:
		return fmt.Sprintf("%s Generating project...", m.spinner.View())
	default:
		return fmt.Sprintf("%s\n", m.finalMsg)
	}
}

func (m model) generateProject() tea.Cmd {
	return func() tea.Msg {
		m.info.name = strings.TrimSpace(m.info.name)
		if len(m.info.name) == 0 {
			m.info.name = m.data.Name.Default
		}

		resp, err := getProjectZip(m.client, m.info)
		if err != nil {
			return errMsg{err}
		}
		ok := resp.StatusCode >= 200 && resp.StatusCode < 300
		if !ok {
			return errMsg{errors.New("failed to generate project")}
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return errMsg{err}
		}

		if err := unzip(body, m.info.name); err != nil {
			return errMsg{err}
		}
		return errMsg{nil}
	}
}

func newForm(info *projectInfo, data *metadata) *huh.Form {
	validate := func(str string) error {
		str = strings.TrimSpace(str)
		if strings.Contains(str, " ") {
			return errors.New("should not contain space")
		}
		return nil
	}

	nameValidate := func(str string) error {
		if err := validate(str); err != nil {
			return err
		}
		str = strings.TrimSpace(str)
		if fs, err := os.Stat(str); !os.IsNotExist(err) {
			d := "file"
			if fs.IsDir() {
				d = "directory"
			}
			return fmt.Errorf("a %s named '%s' already exists", d, str)
		}
		return nil
	}

	getOpts := func(st selectType) []huh.Option[string] {
		var opts []huh.Option[string]
		for _, lv := range st.Values {
			opt := huh.NewOption(lv.Name, lv.Id)
			if lv.Id == st.Default {
				opt = opt.Selected(true)
			}
			opts = append(opts, opt)
		}
		return opts
	}

	getProjectOpts := func(pt projectType) []huh.Option[string] {
		var opts []huh.Option[string]
		for _, lv := range pt.Values {
			if lv.Tags.Format == "project" {
				opt := huh.NewOption(lv.Name, lv.Id)
				if lv.Id == pt.Default {
					opt = opt.Selected(true)
				}
				opts = append(opts, opt)
			}
		}
		return opts
	}

	getDepsOpts := func(mt multiSelectType, bootVersion string) []huh.Option[string] {
		var opts []huh.Option[string]
		for _, values := range mt.Values {
			for _, dep := range values.Values {
				if dep.VersionRange.contains(bootVersion) {
					opts = append(opts, huh.NewOption(dep.Name, dep.Id))
				}
			}
		}
		return opts
	}

	multiSelect := huh.NewMultiSelect[string]().
		Title("Add dependencies").
		Filterable(true).
		Height(22). // show 20 dependencies at once
		Value(&info.dependencies)

	return huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Name of the project").
				Value(&info.name).
				Placeholder(data.Name.Default).
				Validate(nameValidate),

			huh.NewInput().
				Title("Group Id").
				Value(&info.group).
				Placeholder(data.GroupId.Default).
				Validate(validate),

			huh.NewInput().
				Title("Artifact Id").
				Value(&info.artifact).
				Placeholder(data.ArtifactId.Default).
				Validate(validate),

			huh.NewInput().
				Title("Write a short description").
				Value(&info.description).
				Placeholder(data.Description.Default),
		),

		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Pick a language").
				Options(getOpts(data.Language)...).
				Value(&info.language),

			huh.NewSelect[string]().
				Title("Java version").
				Options(getOpts(data.JavaVersion)...).
				Value(&info.javaVersion),

			huh.NewSelect[string]().
				Title("Spring Boot version").
				Options(getOpts(data.BootVersion)...).
				Value(&info.bootVersion).
				Validate(func(version string) error {
					// Select dependencies based on spring boot version.
					// Do not show those depencies which are not compatible
					// to selected boot version.
					// Though this is a validation function for this select field,
					// it has been used to filter the dependencies as there is no
					// method for *huh.MultiSelect to do it in a sane way.
					multiSelect.Options(getDepsOpts(data.Dependencies, version)...)
					return nil
				}),

			huh.NewSelect[string]().
				Title("Type of the project").
				Options(getProjectOpts(data.ProjectType)...).
				Value(&info.projectType),

			huh.NewSelect[string]().
				Title("Packaging type").
				Options(getOpts(data.Packaging)...).
				Value(&info.packaging),
		),

		huh.NewGroup(multiSelect),
	).WithTheme(huh.ThemeDracula())
}

func newSpinner() spinner.Model {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("#bd93f9"))
	return spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(style),
	)
}
