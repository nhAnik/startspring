package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hashicorp/go-version"
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
	ProjectType projectType `json:"type"`

	GroupId      struct{ Default string }
	ArtifactId   struct{ Default string }
	Name         struct{ Default string }
	Description  struct{ Default string }
	Dependencies multiSelectType
}

type selectType struct {
	Default string
	Values  []value
}

type value struct {
	Id   string
	Name string
}

type projectType struct {
	Default string
	Values  []projectValue
}
type projectValue struct {
	value
	Tags struct{ Format string }
}

type multiSelectType struct {
	Values []struct {
		Values []struct {
			Id           string
			Name         string
			VersionRange VersionRange
		}
	}
}

type VersionRange struct {
	Lower, Upper               string
	LowerInclude, UpperInclude bool
}

// contains checks if the version range contains the
// given spring boot version.
func (vr VersionRange) contains(v string) bool {
	if vr.Lower == "" && vr.Upper == "" {
		return true
	}
	bv, _ := version.NewSemver(v)
	lowerOk, upperOk := true, true

	if vr.Lower != "" {
		lv, _ := version.NewSemver(vr.Lower)
		if bv.LessThanOrEqual(lv) || vr.LowerInclude && bv.LessThan(lv) {
			lowerOk = false
		}
	}

	if vr.Upper != "" {
		uv, _ := version.NewSemver(vr.Upper)
		if bv.GreaterThanOrEqual(uv) || vr.UpperInclude && bv.GreaterThan(uv) {
			upperOk = false
		}
	}
	return lowerOk && upperOk
}

func (vr VersionRange) String() string {
	if vr.Lower == "" {
		return ""
	}

	var sb strings.Builder
	sb.WriteByte('>')
	if vr.LowerInclude {
		sb.WriteByte('=')
	}
	sb.WriteString(vr.Lower)

	if vr.Upper != "" {
		sb.WriteString(" and ")
		sb.WriteByte('<')
		if vr.UpperInclude {
			sb.WriteByte('=')
		}
		sb.WriteString(vr.Upper)
	}
	return sb.String()
}

func (vr *VersionRange) UnmarshalJSON(b []byte) error {
	b = b[1 : len(b)-1]

	switch b[0] {
	case '[', '(':
		if b[0] == '[' {
			vr.LowerInclude = true
		}
		for i := 0; i < len(b); i++ {
			if b[i] == ',' {
				vr.Lower = string(b[1:i])
				vr.Upper = string(b[i+1 : len(b)-1])
				break
			}
		}
		if b[len(b)-1] == ']' {
			vr.UpperInclude = true
		}

	default:
		vr.Lower = string(b)
		vr.LowerInclude = true
	}

	return nil
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
	if err := dec.Decode(data); err != nil {
		return nil, err
	}
	return data, nil
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

func main() {
	client := &http.Client{}

	data, err := getMetaData(client)
	if err != nil {
		die(err)
	}

	program := tea.NewProgram(newModel(data, client))
	if _, err := program.Run(); err != nil {
		die(err)
	}
}

func die(err error) {
	fmt.Println(err)
	os.Exit(1)
}
