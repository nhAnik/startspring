package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
)

// projectInfo bundles all the information of
// a spring project.
type projectInfo struct {
	name         string
	group        string
	artifact     string
	description  string
	projectType  string
	language     string
	bootVersion  string
	packaging    string
	javaVersion  string
	dependencies []string
}

// metadata is a struct to decode the json response from
// https://start.spring.io/metadata/client
type metadata struct {
	Language    selectType
	JavaVersion selectType
	BootVersion selectType
	Packaging   selectType
	ProjectType selectType `json:"type"`

	GroupId     struct{ Default string }
	ArtifactId  struct{ Default string }
	Name        struct{ Default string }
	Description struct{ Default string }
}

type selectType struct {
	Default string
	Values  []value
}

type value struct {
	Id   string
	Name string
}

func getMetaData(client *http.Client) (*metadata, error) {
	req, err := http.NewRequest(http.MethodGet, "https://start.spring.io/metadata/client", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Accept", "application/vnd.initializr.v2.2+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	data := &metadata{}
	dec := json.NewDecoder(resp.Body)
	dec.Decode(data)
	return data, nil
}

func getOpts(values []value) []huh.Option[string] {
	var opts []huh.Option[string]
	for _, lv := range values {
		opts = append(opts, huh.NewOption(lv.Name, lv.Id))
	}
	return opts
}

func validationFunc(str string) error {
	str = strings.TrimSpace(str)
	if len(str) == 0 {
		return errors.New("should not be empty")
	}
	if strings.Contains(str, " ") {
		return errors.New("should not contain space")
	}
	return nil
}

func generateForm(info *projectInfo, data *metadata) *huh.Form {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Name of the project:").
				Value(&info.name).
				Placeholder(data.Name.Default).
				Validate(validationFunc),

			huh.NewInput().
				Title("Group Id:").
				Value(&info.group).
				Placeholder(data.GroupId.Default).
				Validate(validationFunc),

			huh.NewInput().
				Title("Artifact Id:").
				Value(&info.artifact).
				Placeholder(data.ArtifactId.Default).
				Validate(validationFunc),

			huh.NewInput().
				Title("Write a short description:").
				Value(&info.description).
				Placeholder(data.Description.Default),
		),

		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Pick a language").
				Options(getOpts(data.Language.Values)...).
				Value(&info.language),

			huh.NewSelect[string]().
				Title("Java version").
				Options(getOpts(data.JavaVersion.Values)...).
				Value(&info.javaVersion),

			huh.NewSelect[string]().
				Title("Spring Boot version").
				Options(getOpts(data.BootVersion.Values)...).
				Value(&info.bootVersion),

			huh.NewSelect[string]().
				Title("Type of the project").
				Options(getOpts(data.ProjectType.Values)...).
				Value(&info.projectType),

			huh.NewSelect[string]().
				Title("Packaging type").
				Options(getOpts(data.Packaging.Values)...).
				Value(&info.packaging),
		),

		// Some basic dependencies have been added as the MultiSelect currently
		// does not work well with long list of dependencies.
		// Also, it lacks support of filtering.
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Add dependencies").
				Options(
					huh.NewOption("Spring Web", "web"),
					huh.NewOption("Spring Reactive Web", "webflux"),
					huh.NewOption("Spring Data JPA", "data-jpa"),
					huh.NewOption("Spring Data JDBC", "data-jdbc"),
					huh.NewOption("Spring Data Redis (Access+Driver)", "data-redis"),
					huh.NewOption("Spring Data MongoDB", "data-mongodb"),
					huh.NewOption("Spring Boot DevTools", "devtools"),
					huh.NewOption("Lombok", "lombok"),
					huh.NewOption("GraalVM Native Support", "native"),
					huh.NewOption("Docker Compose Support", "docker-compose"),
					huh.NewOption("Spring HATEOAS", "hateoas"),
					huh.NewOption("MySQL Driver", "mysql"),
					huh.NewOption("Oracle Driver", "oracle"),
					huh.NewOption("PostgreSQL Driver", "postgresql"),
					huh.NewOption("Thymeleaf", "thymeleaf"),
					huh.NewOption("Mustache", "mustache"),
					huh.NewOption("Spring Security", "security"),
					huh.NewOption("OAuth2 Client", "oauth2-client"),
				).
				Filterable(true).
				Value(&info.dependencies),
		),
	)
}

func getProjectZip(client *http.Client, info *projectInfo) (*http.Response, error) {
	form := url.Values{}
	form.Add("name", info.name)
	form.Add("groupId", info.group)
	form.Add("artifactId", info.artifact)
	form.Add("description", info.description)

	form.Add("language", info.language)
	form.Add("javaVersion", info.javaVersion)
	form.Add("bootVersion", info.bootVersion)
	form.Add("type", info.projectType)
	form.Add("packaging", info.packaging)

	form.Add("dependencies", strings.Join(info.dependencies, ","))

	return client.PostForm("https://start.spring.io/starter.zip", form)
}

func unzip(body []byte, projectName string) error {
	zipReader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	if err := os.Mkdir(filepath.Join(cwd, projectName), 0777); err != nil {
		return err
	}

	for _, zf := range zipReader.File {
		zfReader, err := zf.Open()
		if err != nil {
			return err
		}

		fpath := filepath.Join(cwd, projectName, zf.Name)
		if zf.FileInfo().IsDir() {
			err = os.MkdirAll(fpath, zf.Mode())
			if err != nil {
				return err
			}
		} else {
			fdir := filepath.Dir(fpath)

			err = os.MkdirAll(fdir, zf.Mode())
			if err != nil {
				return err
			}

			f, err := os.OpenFile(
				fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, zf.Mode())
			if err != nil {
				return err
			}
			defer f.Close()

			_, err = io.Copy(f, zfReader)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func generateProject(client *http.Client, info *projectInfo) error {
	resp, err := getProjectZip(client, info)
	if err != nil {
		return err
	}
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	if !ok {
		return errors.New("failed to generate project")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if err := unzip(body, info.name); err != nil {
		return err
	}

	return nil
}

func main() {
	client := &http.Client{}

	data, err := getMetaData(client)
	if err != nil {
		die(err)
	}

	info := &projectInfo{}
	form := generateForm(info, data)
	if err := form.Run(); err != nil {
		die(err)
	}

	var genProjectErr error
	sp := spinner.New().
		Title("Generating project...").
		Action(func() {
			genProjectErr = generateProject(client, info)
		})

	if err := sp.Run(); err != nil {
		die(err)
	}

	if genProjectErr != nil {
		die(genProjectErr)
	}

	fmt.Println("Project generated")
}

func die(err error) {
	fmt.Println(err)
	os.Exit(1)
}
