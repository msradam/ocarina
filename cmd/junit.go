package cmd

import (
	"encoding/xml"
)

// JUnit XML is the format every CI test dashboard ingests (GitLab reports,
// Jenkins JUnit plugin, the common GitHub Actions reporters). One rondo is one
// <testsuite>; each executed step is a <testcase>.
type junitTestsuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Name     string           `xml:"name,attr"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Skipped  int              `xml:"skipped,attr"`
	Time     float64          `xml:"time,attr"`
	Suites   []junitTestsuite `xml:"testsuite"`
}

type junitTestsuite struct {
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Skipped  int             `xml:"skipped,attr"`
	Time     float64         `xml:"time,attr"`
	Cases    []junitTestcase `xml:"testcase"`
}

type junitTestcase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      float64       `xml:"time,attr"`
	Failure   *junitMessage `xml:"failure,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
}

type junitMessage struct {
	Message string `xml:"message,attr"`
}

type junitSkipped struct {
	Message string `xml:"message,attr,omitempty"`
}

func junitXML(suiteName string, r runResult) ([]byte, error) {
	suite := junitTestsuite{
		Name:     suiteName,
		Tests:    r.Total,
		Failures: r.Failed,
		Skipped:  r.Skipped,
		Time:     float64(r.DurationMS) / 1000,
	}
	for _, s := range r.Steps {
		cls := s.Server
		if cls == "" {
			cls = "ocarina"
		}
		tc := junitTestcase{
			Name:      s.Name,
			Classname: cls,
			Time:      float64(s.DurationMS) / 1000,
		}
		switch s.Status {
		case "failed":
			tc.Failure = &junitMessage{Message: s.Message}
		case "skipped":
			tc.Skipped = &junitSkipped{Message: s.Message}
		}
		suite.Cases = append(suite.Cases, tc)
	}

	doc := junitTestsuites{
		Name:     suiteName,
		Tests:    r.Total,
		Failures: r.Failed,
		Skipped:  r.Skipped,
		Time:     suite.Time,
		Suites:   []junitTestsuite{suite},
	}
	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(out, '\n')...), nil
}
