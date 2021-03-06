package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

// Contribution describes a contribution.
type Contribution struct {
	Login   string
	Name    string
	Email   string
	Commits int
}

var (
	regexMailmap      = regexp.MustCompile("^()([^<]*) +<([^>]*)>$")
	regexContribution = regexp.MustCompile("^([0-9]+)\\t([^<]*) +<([^>]*)>$")

	bot = "fireworq.github@gmail.com"

	// These are commits before the OSS release.
	initial = map[string]int{
		"tarao.gnn@gmail.com":              559,
		"shibayu36@gmail.com":              31,
		"hakobe@gmail.com":                 28,
		"yuki.tsubo@gmail.com":             25,
		"fly.me.to.the.moon1204@gmail.com": 17,
	}

	markdown = template.Must(template.New("markdown").Parse(`
{{- /* */ -}}
<!-- DO NOT MODIFY : this file is automatically generated by script/genauthors/genauthors.go -->

# Authors

This is the official list of Fireworq authors for copyright purposes.

## Individual Persons

|Name |E-mail  |GitHub|Commits |
|:----|:-------|:-----|-------:|
{{range .contributions -}}
|{{.Name}}|<{{.Email}}>|{{if ne .Login ""}}[@{{.Login}}](https://github.com/{{.Login}}){{end}}|{{.Commits}}|
{{end}}

{{- /* */}}
Items are automatically added by ` + "`" + `git shortlog -sne` + "`" + `.  Please add rules to [` + "`" + `.mailmap` + "`" + `](.mailmap) to keep it canonical (see "MAPPING AUTHORS" section of ` + "`" + `git help shortlog` + "`" + ` for the notation of the rules).  E-mail field must match your public E-mail setting on your GitHub account if you wish to show your GitHub account name.

## Organizations

- [Hatena Co., Ltd.](http://hatenacorp.jp/)

`))

	plain = template.Must(template.New("plain").Parse(`
{{- /* */ -}}
# This is the official list of Fireworq authors for copyright purposes.

# This file is automatically generated by
# script/genauthors/genauthors.go.  Items are automatically added by
# git shortlog -sne.  Please add rules to .mailmap to keep it
# canonical (see "MAPPING AUTHORS" section of git help shortlog for
# the notation of the rules).

# Individual Persons

{{range .contributions -}}
{{.Name}} <{{.Email}}>
{{end}}

{{- /* */}}
## Organizations

Hatena Co., Ltd.
`))
)

func main() {
	format := flag.String("format", "plain", "The output format (markdown or plain).")
	flag.Parse()

	list, err := exec.Command("/bin/bash", "-c", "git log | git shortlog -sne").Output()
	if err != nil {
		log.Fatal(err)
	}

	contributions := make(map[string]*Contribution)

	mailmap, err := ioutil.ReadFile(".mailmap")
	if err != nil {
		log.Fatal(err)
	}
	for _, line := range strings.Split(string(mailmap), "\n") {
		c := parseContribution(regexMailmap, line)
		if c == nil {
			continue
		}
		if c.Commits <= 0 {
			continue
		}

		if _, ok := contributions[c.Email]; !ok {
			contributions[c.Email] = c
		}
	}

	for _, line := range strings.Split(string(list), "\n") {
		c := parseContribution(regexContribution, line)
		if c == nil {
			continue
		}
		if c.Commits <= 0 {
			continue
		}

		if initial, ok := contributions[c.Email]; ok {
			initial.Commits += c.Commits
		} else {
			contributions[c.Email] = c
		}
	}

	items := make([]*Contribution, 0, len(contributions))
	emails := make([]string, 0, len(contributions))
	for k, v := range contributions {
		items = append(items, v)
		emails = append(emails, k)
	}

	sort.Slice(items, func(i, j int) bool {
		diff := items[i].Commits - items[j].Commits
		return diff > 0 || (diff == 0 && items[i].Email < items[j].Email)
	})

	tmpl := plain
	if *format == "markdown" {
		gitHubUsers := getGitHubLoginNames(emails)
		for _, c := range contributions {
			if login, ok := gitHubUsers[c.Email]; ok {
				c.Login = login
			}
		}
		tmpl = markdown
	}

	tmpl.Execute(os.Stdout, map[string][]*Contribution{
		"contributions": items,
	})
}

func parseContribution(regex *regexp.Regexp, line string) *Contribution {
	line = strings.TrimSpace(line)
	if len(line) <= 0 {
		return nil
	}

	m := regex.FindStringSubmatch(line)
	if m == nil || len(m) < 4 {
		return nil
	}

	email := m[3]
	if email == bot {
		return nil
	}

	var commits int
	if m[1] != "" {
		var err error
		commits, err = strconv.Atoi(m[1])
		if err != nil {
			return nil
		}
	} else if i, ok := initial[email]; ok {
		commits = i
	}

	return &Contribution{
		Commits: commits,
		Name:    m[2],
		Email:   email,
	}
}

func getGitHubLoginNames(emails []string) map[string]string {
	result := make(map[string]string)
	for i := 0; i < len(emails); i += 5 {
		end := i + 5
		if end > len(emails) {
			end = len(emails)
		}
		if r, _ := getGitHubLoginNames1(emails[i:end]); r != nil {
			for k, v := range r {
				if _, ok := result[k]; !ok {
					result[k] = v
				}
			}
		}
	}
	return result
}

func getGitHubLoginNames1(emails []string) (map[string]string, error) {
	r := make(map[string]string)
	if len(emails) <= 0 {
		return r, nil
	}

	queries := make([]string, 0, len(emails))
	for _, email := range emails {
		queries = append(queries, url.PathEscape(email)+"%20in:email")
	}

	req, err := http.NewRequest(
		"GET",
		"https://api.github.com/search/users?q="+strings.Join(queries, "%20OR%20"),
		nil,
	)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Accept", "application/vnd.github.v3.text-match+json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rslt GitHubUserSearchResponse
	err = json.Unmarshal(body, &rslt)
	if err != nil {
		return nil, err
	}

	for _, u := range rslt.Items {
		for _, m := range u.Matches {
			if _, ok := r[m.Fragment]; !ok {
				r[m.Fragment] = u.Login
			}
		}
	}

	return r, nil
}

// GitHubUserSearchResponse describes a GitHub user search result.
type GitHubUserSearchResponse struct {
	Items []GitHubUser `json:"items"`
}

// GitHubUser describes a GitHub user search result item.
type GitHubUser struct {
	Login   string            `json:"login"`
	Matches []GitHubUserMatch `json:"text_matches"`
}

// GitHubUserMatch describes a matching part in a GitHub user search result item.
type GitHubUserMatch struct {
	Property string `json:"property"`
	Fragment string `json:"fragment"`
}
