// This file is part of arduino-cli.
//
// Copyright 2020 ARDUINO SA (http://www.arduino.cc/)
//
// This software is released under the GNU General Public License version 3,
// which covers the main part of arduino-cli.
// The terms of this license can be found at:
// https://www.gnu.org/licenses/gpl-3.0.en.html
//
// You can be released from the requirements of the above licenses by purchasing
// a commercial license. Buying such a license is mandatory if you want to
// modify or otherwise use the software for commercial activities involving the
// Arduino software without disclosing the source code of your own applications.
// To purchase a commercial license, send an email to license@arduino.cc.

package lib

import (
	"context"
	"sort"
	"strings"

	"github.com/arduino/arduino-cli/arduino"
	"github.com/arduino/arduino-cli/arduino/libraries/librariesindex"
	"github.com/arduino/arduino-cli/arduino/libraries/librariesmanager"
	"github.com/arduino/arduino-cli/arduino/utils"
	"github.com/arduino/arduino-cli/commands/internal/instances"
	rpc "github.com/arduino/arduino-cli/rpc/cc/arduino/cli/commands/v1"
	semver "go.bug.st/relaxed-semver"
)

// LibrarySearch FIXMEDOC
func LibrarySearch(ctx context.Context, req *rpc.LibrarySearchRequest) (*rpc.LibrarySearchResponse, error) {
	lm := instances.GetLibraryManager(req.GetInstance())
	if lm == nil {
		return nil, &arduino.InvalidInstanceError{}
	}
	return searchLibrary(req, lm), nil
}

// matcherTokensFromQueryString parses the query string into tokens of interest
// for the qualifier-value pattern matching.
func matcherTokensFromQueryString(query string) []string {
	escaped := false
	quoted := false
	tokens := []string{}
	sb := &strings.Builder{}

	for _, r := range query {
		// Short circuit the loop on backslash so that all other paths can clear
		// the escaped flag.
		if !escaped && r == '\\' {
			escaped = true
			continue
		}

		if r == '"' {
			if !escaped {
				quoted = !quoted
			} else {
				sb.WriteRune(r)
			}
		} else if !quoted && r == ' ' {
			tokens = append(tokens, strings.ToLower(sb.String()))
			sb.Reset()
		} else {
			sb.WriteRune(r)
		}
		escaped = false
	}
	if sb.Len() > 0 {
		tokens = append(tokens, strings.ToLower(sb.String()))
	}

	return tokens
}

// defaulLibraryMatchExtractor returns a string describing the library that
// is used for the simple search.
func defaultLibraryMatchExtractor(lib *librariesindex.Library) string {
	res := lib.Name + " " +
		lib.Latest.Paragraph + " " +
		lib.Latest.Sentence + " " +
		lib.Latest.Author + " "
	for _, include := range lib.Latest.ProvidesIncludes {
		res += include + " "
	}
	return res
}

var qualifiers map[string]func(lib *librariesindex.Library) string = map[string]func(lib *librariesindex.Library) string{
	"name":          func(lib *librariesindex.Library) string { return lib.Name },
	"architectures": func(lib *librariesindex.Library) string { return strings.Join(lib.Latest.Architectures, " ") },
	"author":        func(lib *librariesindex.Library) string { return lib.Latest.Author },
	"category":      func(lib *librariesindex.Library) string { return lib.Latest.Category },
	"dependencies": func(lib *librariesindex.Library) string {
		names := make([]string, len(lib.Latest.Dependencies))
		for i, dep := range lib.Latest.Dependencies {
			names[i] = dep.GetName()
		}
		return strings.Join(names, " ")
	},
	"maintainer": func(lib *librariesindex.Library) string { return lib.Latest.Maintainer },
	"paragraph":  func(lib *librariesindex.Library) string { return lib.Latest.Paragraph },
	"sentence":   func(lib *librariesindex.Library) string { return lib.Latest.Sentence },
	"types":      func(lib *librariesindex.Library) string { return strings.Join(lib.Latest.Types, " ") },
	"version":    func(lib *librariesindex.Library) string { return lib.Latest.Version.String() },
	"website":    func(lib *librariesindex.Library) string { return lib.Latest.Website },
}

// matcherFromQueryString returns a closure that takes a library as a
// parameter and returns true if the library matches the query.
func matcherFromQueryString(query string) func(*librariesindex.Library) bool {
	// A qv-query is one using <qualifier>[:=]<value> syntax.
	qvQuery := strings.Contains(query, ":") || strings.Contains(query, "=")

	if !qvQuery {
		queryTerms := utils.SearchTermsFromQueryString(query)
		return func(lib *librariesindex.Library) bool {
			return utils.Match(defaultLibraryMatchExtractor(lib), queryTerms)
		}
	}

	queryTerms := matcherTokensFromQueryString(query)

	return func(lib *librariesindex.Library) bool {
		matched := true
		for _, term := range queryTerms {

			if sepIdx := strings.IndexAny(term, "=:"); sepIdx != -1 {
				potentialKey := term[:sepIdx]
				separator := term[sepIdx]

				extractor, ok := qualifiers[potentialKey]
				if ok {
					target := term[sepIdx+1:]
					if separator == ':' {
						matched = (matched && utils.Match(extractor(lib), []string{target}))
					} else { // "="
						matched = (matched && strings.ToLower(extractor(lib)) == target)
					}
				} else {
					// Unknown qualifier names revert to basic search terms.
					matched = (matched && utils.Match(defaultLibraryMatchExtractor(lib), []string{term}))
				}
			} else {
				// Terms that do not use qv-syntax are handled as usual.
				matched = (matched && utils.Match(defaultLibraryMatchExtractor(lib), []string{term}))
			}
		}
		return matched
	}
}

func searchLibrary(req *rpc.LibrarySearchRequest, lm *librariesmanager.LibrariesManager) *rpc.LibrarySearchResponse {
	res := []*rpc.SearchedLibrary{}
	query := req.GetSearchArgs()
	if query == "" {
		query = req.GetQuery()
	}

	matcher := matcherFromQueryString(query)

	for _, lib := range lm.Index.Libraries {
		if matcher(lib) {
			res = append(res, indexLibraryToRPCSearchLibrary(lib, req.GetOmitReleasesDetails()))
		}
	}

	// get a sorted slice of results
	sort.Slice(res, func(i, j int) bool {
		// Sort by name, but bubble up exact matches
		equalsI := strings.EqualFold(res[i].Name, query)
		equalsJ := strings.EqualFold(res[j].Name, query)
		if equalsI && !equalsJ {
			return true
		} else if !equalsI && equalsJ {
			return false
		}
		return res[i].Name < res[j].Name
	})

	return &rpc.LibrarySearchResponse{Libraries: res, Status: rpc.LibrarySearchStatus_LIBRARY_SEARCH_STATUS_SUCCESS}
}

// indexLibraryToRPCSearchLibrary converts a librariindex.Library to rpc.SearchLibrary
func indexLibraryToRPCSearchLibrary(lib *librariesindex.Library, omitReleasesDetails bool) *rpc.SearchedLibrary {
	var releases map[string]*rpc.LibraryRelease
	if !omitReleasesDetails {
		releases = map[string]*rpc.LibraryRelease{}
		for _, rel := range lib.Releases {
			releases[rel.Version.String()] = getLibraryParameters(rel)
		}
	}

	versions := semver.List{}
	for _, rel := range lib.Releases {
		versions = append(versions, rel.Version)
	}
	sort.Sort(versions)

	versionsString := []string{}
	for _, v := range versions {
		versionsString = append(versionsString, v.String())
	}

	return &rpc.SearchedLibrary{
		Name:              lib.Name,
		Releases:          releases,
		Latest:            getLibraryParameters(lib.Latest),
		AvailableVersions: versionsString,
	}
}

// getLibraryParameters FIXMEDOC
func getLibraryParameters(rel *librariesindex.Release) *rpc.LibraryRelease {
	return &rpc.LibraryRelease{
		Author:           rel.Author,
		Version:          rel.Version.String(),
		Maintainer:       rel.Maintainer,
		Sentence:         rel.Sentence,
		Paragraph:        rel.Paragraph,
		Website:          rel.Website,
		Category:         rel.Category,
		Architectures:    rel.Architectures,
		Types:            rel.Types,
		License:          rel.License,
		ProvidesIncludes: rel.ProvidesIncludes,
		Dependencies:     getLibraryDependenciesParameter(rel.GetDependencies()),
		Resources: &rpc.DownloadResource{
			Url:             rel.Resource.URL,
			ArchiveFilename: rel.Resource.ArchiveFileName,
			Checksum:        rel.Resource.Checksum,
			Size:            rel.Resource.Size,
			CachePath:       rel.Resource.CachePath,
		},
	}
}

func getLibraryDependenciesParameter(deps []semver.Dependency) []*rpc.LibraryDependency {
	res := []*rpc.LibraryDependency{}
	for _, dep := range deps {
		res = append(res, &rpc.LibraryDependency{
			Name:              dep.GetName(),
			VersionConstraint: dep.GetConstraint().String(),
		})
	}
	return res
}
