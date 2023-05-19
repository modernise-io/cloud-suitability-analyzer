/*******************************************************************************
 * Copyright (c) 2018 - Present VMware, Inc. All Rights Reserved.
 * SPDX-License-Identifier: BSD-2
 ******************************************************************************/

package csa

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	//"github.com/inancgumus/screen"
	"csa-app/db"
	"csa-app/gocloc"
	"csa-app/model"
	"csa-app/util"
	"time"

	"database/sql"

	"github.com/antchfx/xmlquery"

	//"github.com/mattn/go-sqlite3"
	"gopkg.in/yaml.v3"
)

func (csaService *CsaService) processFile(run *model.Run, app *model.Application, file *util.FileInfo, rules []model.Rule, hasContentRules bool, output chan<- interface{}) (findingCnt int, err error) {
	
	if len(rules) > 0 {

		//Get File Lang
		lang, redactComments := csaService.fileUtil.GetLangForFileExt(file.GetCleanedExt())

		redactComments = redactComments && !*util.DisableIgnoreComments

		if redactComments && *util.Verbose {
			fmt.Printf("Determined File [%s] contains language [%s]\n", file.Name, lang.Name)
		}

		inFile, err := os.Open(file.FQN)

		if err != nil {
			util.TrackError("Analysis", err)
			if *util.Verbose {
				_, _ = fmt.Fprintf(os.Stderr, "Failed opening file [%s]. Details: %s\n", file.FQN, err.Error())
				fmt.Printf("************** WARNING - INCONSISTENT RESULTS CAN OCCUR BECAUSE OF THIS ERROR **************\n")
			}
			return findingCnt, err
		}

		defer closeFile(inFile)

		line := 0
		sloc := 0
		scanner := bufio.NewScanner(inFile)
		buf := make([]byte, run.LineBufferSize)
		scanner.Buffer(buf, util.MAX_LINE_BUFFER_SIZE)

		midComment := false
		process := true
		contents := ""

		for scanner.Scan() {

			//Count line regardless if comment
			line++
			curLine := scanner.Text()

			//Only accumulate if we have content rules to process
			if hasContentRules {
				contents += curLine + "\n"
			}

			if redactComments {
				curLine, process, midComment = util.HandleComments(curLine, midComment, lang)
			}
			
			if process && len(strings.TrimSpace(curLine)) > 0 {
				sloc++
				for i := range rules {
					
					if rules[i].Target == model.LINE_TARGET {
						findingCnt += csaService.processPatterns(run, app, file, line, curLine, rules[i], output)

						if *util.Verbose {
							util.WriteLog("A10nalyzing", "### Rule: %s Hit: %d times on File: %s Line: %d ###\n", rules[i].Name, findingCnt, file.Name, line)
						}
					}
				}
			}
		}

		//Only reprocess if necessary
		if hasContentRules {
			for i := range rules {
				if rules[i].Target == model.CONTENTS_TARGET {
					findingCnt += csaService.processPatterns(run, app, file, 0, contents, rules[i], output)
					if *util.Verbose {
						util.WriteLog("A11nalyzing", "### Rule: %s Hit: %d times on File: %s  ###\n", rules[i].Name, findingCnt, file.Name)
					}
				}
			}
		}

		//Create an info finding for each file with SLOC info!
		fileFinding := model.Finding{
			RunID:       run.ID,
			Filename:    file.Name,
			Fqn:         file.FQN,
			Ext:         file.Ext,
			Category:    model.SLOC_CATEGORY,
			Pattern:     model.FILE_SLOC_PATTERN,
			Value:       fmt.Sprintf("%d", sloc),
			Effort:      0,
			Readiness:   0,
			Criticality: "none",
			Application: file.Dir}

		fileFinding.AddTag(model.INFO_FINDING)
		fileFinding.AddTag(model.SLOC_CATEGORY)

		//Send "file" finding to save worker
		output <- fileFinding

		run.AddFindings(1)
	}

	return findingCnt, nil
}

func (csaService *CsaService) handleRuleMatched(run *model.Run, app *model.Application, file *util.FileInfo, line int, target string, rule model.Rule, pattern model.Pattern, output chan<- interface{}, result string, finding *model.Finding) {
	matchHasImpact := true

	//Track Rule matches for rules that have the associated impact type. Otherwise that is a waste of time!
	if rule.Impact == model.APP_IMPACT {
		app.Lock()
		mCnt, ruleHitBefore := app.MatchedRules[rule.Name]
		app.MatchedRules[rule.Name] = mCnt + 1
		app.Unlock()
		matchHasImpact = !ruleHitBefore
	} else if rule.Impact == model.FILE_IMPACT {
		file.Lock()
		mCnt, ruleHitBefore := file.MatchedRules[rule.Name]
		file.MatchedRules[rule.Name] = mCnt + 1
		file.Unlock()
		matchHasImpact = !ruleHitBefore
	}

	effort := rule.Effort
	if pattern.Effort != 0 {
		effort = pattern.Effort
	}

	readiness := rule.Readiness
	if pattern.Readiness > 0 {
		readiness = pattern.Readiness
	}

	criticality := rule.Criticality
	if pattern.Criticality != "" {
		criticality = pattern.Criticality
	}

	category := rule.Category
	if pattern.Category != "" {
		category = pattern.Category
	}

	note := ""
	if !matchHasImpact {
		note = fmt.Sprintf("Original effort [%d] of finding was squashed by rule impact setting of [%s]", effort, rule.Impact)
		effort = 0
		readiness = 0
	}

	data := model.Finding{
		RunID:       run.ID,
		Filename:    file.Name,
		Fqn:         file.FQN,
		Ext:         file.Ext,
		Line:        line,
		Rule:        rule.Name,
		Pattern:     pattern.Value,
		Category:    category,
		Effort:      effort,
		Note:        note,
		Result:      result,
		Readiness:   readiness,
		Criticality: criticality,
		Application: file.Dir}

	if finding != nil {
		data.Filename = finding.Filename
		data.Fqn = finding.Fqn
		data.Ext = finding.Ext
		data.Advice = finding.Advice
		data.Line = finding.Line
		data.Value = finding.Value
	} else {
		data.SetValue(target)
	}

	if data.Advice == "" {
		data.Advice = rule.Advice

		if pattern.Advice != "" {
			data.Advice = pattern.Advice
		}
	}

	//Add Tags from rule & pattern
	for _, tag := range rule.Tags {
		data.AddTag(tag.Value)
		app.AssociateTag(model.ApplicationTag{Value: tag.Value})
	}

	if pattern.Tag != "" {
		data.AddTag(pattern.Tag)
		app.AssociateTag(model.ApplicationTag{Value: pattern.Tag})
	}

	//Add Recipes
	for _, recipe := range rule.Recipes {
		data.AddRecipe(recipe.URI)
	}

	if pattern.Recipe != "" {
		data.AddRecipe(pattern.Recipe)
	}

	//Send finding to save worker
	output <- data
}

func (csaService *CsaService) RunPlugin(run *model.Run, app *model.Application, file *util.FileInfo, line int, target string, rule model.Rule, pattern model.Pattern, output chan<- interface{}) {
	commandTokens := regexp.MustCompile("\\s+").Split(pattern.Command, -1)
	command := commandTokens[0]
	args := append(commandTokens[1:], file.FQN)
	cmd := exec.Command("plugins"+string(os.PathSeparator)+command, args...)

	if stdout, err := cmd.StdoutPipe(); err == nil {
		if stderr, err := cmd.StderrPipe(); err == nil {
			if err = cmd.Start(); err == nil {
				decoder := json.NewDecoder(stdout)
				var finding model.Finding

				for ; err != io.EOF; err = decoder.Decode(&finding) {
					csaService.handleRuleMatched(run, app, file, line, target, rule, pattern, output, "", &finding)
				}

				scanner := bufio.NewScanner(stderr)

				for scanner.Scan() {
					fmt.Fprintln(os.Stderr, "### Plugin "+command+" stderr: "+scanner.Text())
				}

				if err = cmd.Wait(); err != nil {
					fmt.Fprintln(os.Stderr, "Unable to end plugin")
					fmt.Fprintln(os.Stderr, err)
				}
			} else {
				fmt.Fprintln(os.Stderr, "Unable to start plugin")
				fmt.Fprintln(os.Stderr, err)
			}
		} else {
			fmt.Fprintln(os.Stderr, "Unable to open plugin's stdout")
			fmt.Fprintln(os.Stderr, err)
		}
	} else {
		fmt.Fprintln(os.Stderr, "Unable to open plugin's stderr")
		fmt.Fprintln(os.Stderr, err)
	}
}

func (csaService *CsaService) processPatterns(run *model.Run, app *model.Application, file *util.FileInfo, line int, target string, rule model.Rule, output chan<- interface{}) int {
	if *util.Verbose {
		fmt.Printf("Rule [%s|%s] checking against Value [%s]\n", rule.Name, rule.FileType, target)
	}

	findings := 0

	start := time.Now()

	cnt := int64(0)
	pcnt := int64(0)

	//Process Patterns against line
	for i := range rule.Patterns {
		if *util.Verbose {
			fmt.Printf("\tRule [%s|%s] Pattern [%s] checking against Value [%s]\n", rule.Name, rule.FileType, rule.Patterns[i].Pattern, target)
		}

		if rule.Patterns[i].Type == model.PLUGIN_MATCH_TYPE {
			csaService.RunPlugin(run, app, file, line, target, rule, rule.Patterns[i], output)
			// RunPlugin(run, app, file, line, target, rule, pattern, output, Value, file.FQN)
		} else {
			matchFunc := func() (bool, string) {
				return rule.Patterns[i].Match(target)
			}

			if rule.Patterns[i].Type == model.XPATH_MATCH_TYPE {
				csaService.xmlMux.Lock()

				if csaService.xmlDocs[file.FQN] == nil {
					if rawData, err := ioutil.ReadFile(file.FQN); err == nil {
						if xml, err := xmlquery.Parse(bytes.NewReader(rawData)); err == nil {
							csaService.xmlDocs[file.FQN] = xml
						}
					}
				}

				csaService.xmlMux.Unlock()

				matchFunc = func() (bool, string) {
					return rule.Patterns[i].MatchXml(csaService.xmlDocs[file.FQN])
				}
			} else if rule.Patterns[i].Type == model.YAMLPATH_MATCH_TYPE {
				csaService.yamlMux.Lock()

				if csaService.yamlDocs[file.FQN] == nil {
					if rawData, err := ioutil.ReadFile(file.FQN); err == nil {
						var node yaml.Node
						err = yaml.Unmarshal(rawData, &node)

						if err == nil {
							csaService.yamlDocs[file.FQN] = &node
						}
					}
				}

				csaService.yamlMux.Unlock()

				matchFunc = func() (bool, string) {
					return rule.Patterns[i].MatchYaml(csaService.yamlDocs[file.FQN])
				}
			}

			// if value, ok := path.String(root); ok
			ok, result := matchFunc()
			if ok && !rule.Negative {
				if len(result) > 0 {
					target = regexp.MustCompile(`\r?\n`).ReplaceAllString(result, " ")
				}
				// Lets check if the result is valid
				exclude := csaService.shouldFindingBeExcluded(target, rule)

				if exclude == false {
				   csaService.handleRuleMatched(run, app, file, line, target, rule, rule.Patterns[i], output, result, nil)
				   findings++
				   cnt++
				}
				
			} else if !ok && rule.Negative {
                // Lets check if the result is valid
				exclude := csaService.shouldFindingBeExcluded(target, rule)
				if exclude == false {
				    csaService.handleRuleMatched(run, app, file, 0, target, rule, rule.Patterns[i], output, "", nil)
					findings++
					cnt++
				}

			}

		}

		pcnt++

	} //End Pattern For Loop

	run.AddFindings(findings)
	rule.Metric.Accumulate(pcnt, cnt, time.Since(start))

	return findings
}

func (csaService *CsaService) shouldFindingBeExcluded(target string, rule model.Rule) (exclude bool){
	exclude = false
	if rule.Excludepatterns != nil {
		for j := range rule.Excludepatterns {
			regex := regexp.MustCompile(rule.Excludepatterns[j].Value)
			findingExclude := regex.MatchString(target)
			if findingExclude == true {
				fmt.Println("Finding by rule "+rule.Name+" excluded by ExcludePattern => " + target)
				exclude = true
			}
		}
	}
	return exclude
}

func (csaService *CsaService) scoreApps(run *model.Run) {

	run.StartActivity("scoring")

	appDetails, err := csaService.findingRepository.GetApplicationDetailsForRun(run.ID, 10, 1, false)

	failed := false

	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Unabled to obtain application scores! Details: %v\n", err)
	} else {
		for i := range run.Applications {
			for _, details := range appDetails {
				if run.Applications[i].Name == details.Application {
					run.Applications[i].MergeDetails(details)
					err = run.Applications[i].CalculateScore(nil)
					break
				}
			}

			if err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "Scoring App [%s] failed! Details: %s\n", run.Applications[i].Name, err.Error())
				failed = true
			}
		}
	}

	if failed {
		run.StopActivityLF("scoring", "Scoring...failed!", false, true)
	} else {
		run.StopActivityLF("scoring", "Scoring...done!", false, true)
	}
}

func (csaService *CsaService) generateSloc(run *model.Run) {
	run.StartActivity("sloc")
	for _, config := range run.Applications {
		csaService.gatherSLOCForApp(run, config)
	}
	if !*util.Xtract {
		run.StopActivityLF("sloc", "SLOC Analysis...done!", false, true)
	}
	if *util.Xtract {
		run.StopActivityLF("sloc", "", false, false)
		//screen.Clear()
	}

}

func (csaService *CsaService) getRules(run *model.Run, config *model.ApplicationConfig) (rules []model.Rule, err error) {
	//Include overrules anything
	if config.RuleIncludeTags != "" {
		fmt.Printf("Using only rules with tags [%s]\n", config.RuleIncludeTags)
		return csaService.ruleRepository.GetRulesForRunRestricted(run, strings.Split(config.RuleIncludeTags, ","), false)
	} else if config.RuleExcludeTags != "" {
		fmt.Printf("Using only rules without tags [%s]\n", config.RuleExcludeTags)
		return csaService.ruleRepository.GetRulesForRunRestricted(run, strings.Split(config.RuleExcludeTags, ","), true)
	}
	return csaService.ruleRepository.GetRulesForRun(run)
}

func (csaService *CsaService) gatherSLOCForApp(run *model.Run, app *model.Application) {
	util.WriteLogWithToken("SLOC Analysis", " ", "Running CLOC Embedded for Run [%d]", run.ID)

	clocData := gocloc.ClocEmbeddedByApp(app)
	if len(clocData.UnknownExts) > 0 {
		run.UnknownExts = append(run.UnknownExts, clocData.UnknownExts...)
	}
	appTotal := make(map[string]*util.Language)
	if clocData.ErrorMsg == "" {
		//Write Results to DB!
		for _, domain := range clocData.Domains {
			for _, langTotal := range domain {
				if _, ok := appTotal[langTotal.Name]; !ok {
					appTotal[langTotal.Name] = langTotal
				} else {
					appTotal[langTotal.Name].Files = append(appTotal[langTotal.Name].Files, langTotal.Files...)
					appTotal[langTotal.Name].Blanks += langTotal.Blanks
					appTotal[langTotal.Name].Code += langTotal.Code
					appTotal[langTotal.Name].Comments += langTotal.Comments
				}
			}
		}

		for _, langTotal := range appTotal {
			_ = csaService.slocRepository.CreateSlocData(&model.RunSloc{RunID: run.ID, Application: app.Name, Lang: langTotal.Name,
				TotalFiles: len(langTotal.Files), BlankLines: int(langTotal.Blanks),
				CommentLines: int(langTotal.Comments), CodeLines: int(langTotal.Code)})
		}

	} else {
		fmt.Println(clocData.ErrorMsg)
	}

}

func (csaService *CsaService) generateReports(run *model.Run) {
	if *util.OutputReports {
		run.StartActivity("reports")
		csaService.reportService.GenerateReports(run)
		run.StopActivityLF("reports", "\nReport Generation...done!", false, true)
	} else {
		csaService.reportService.GenerateClocReport(run, true)
		csaService.genAppCSAResults(run)
	}
}

func (csaService *CsaService) genRuleMetrics(run *model.Run) {

	headers := []string{"name", "criticality", "rule-checks", "pattern-checks", "hits", "total-time", "longest", "shortest", "rule-avg", "pattern-avg", "hit-avg"}
	var data [][]string

	appMetrics := make(map[string]model.KV)

	for _, app := range run.Applications {
		for _, rule := range app.Rules {
			if _, found := appMetrics[rule.Name]; !found {
				appMetrics[rule.Name] = model.KV{Key: rule.Name, Value: rule.Metric}
			} else {
				appMetrics[rule.Name].Value.Merge(rule.Metric)
			}
		}
	}

	var metrics []model.KV
	for _, metric := range appMetrics {
		metrics = append(metrics, metric)
	}

	sort.Sort(model.MetricByCriticalityHitsAndTime(metrics))

	for _, metric := range metrics {

		db.SaveMetric(metric)

		line := []string{metric.Value.Rule, metric.Value.RuleCriticality, fmt.Sprint(metric.Value.Checks), fmt.Sprint(metric.Value.PatternChecks),
			fmt.Sprint(metric.Value.Hits), metric.Value.TotalTime.String(), metric.Value.Longest.String(),
			metric.Value.Shortest.String(), metric.Value.AvgRule.String(), metric.Value.AvgPat.String(),
			metric.Value.AvgHit.String()}

		data = append(data, line)
	}

	if *util.DumpRuleMetrics {
		csaService.reportService.DisplayReport(headers, data, "Rule Metrics", false)
	}
}

func (csaService *CsaService) genAppCSAResults(run *model.Run) {

	headers := []string{"name", "files analyzed", "files ignored", "sloc cnt", "# findings", "scoring-model", "score", "recommendation"}
	var data [][]string

	//--- TODO: Move to function

	if util.ReportDir != nil {
		qryFindings := `
			SELECT 
			    run_id,
				application,
				filename,
				fqn,
				line,
				rule,
				advice,
				effort,
				category,
				criticality,
				ext,
				pattern,
				value,
				readiness
			FROM findings where rule is not null and rule != ''
		`

		var sqlDBFile = *util.DbDir + string(os.PathSeparator) + *util.DBName

		fileFindings := []model.Finding{}

		if err := os.MkdirAll(*util.ReportDir, os.ModePerm); err != nil {
			fmt.Print(err)
			os.Exit(1)
		}

		db, err := sql.Open("sqlite3", sqlDBFile)
		if err != nil {
			fmt.Print(err)
			os.Exit(1)
		}
		defer db.Close()
		rows, err := db.Query(qryFindings)
		if err != nil {
			fmt.Print(err)
			os.Exit(1)
		}
		defer rows.Close()

		var application string
		var filename string
		var fqn string
		var line int
		var runId uint
		var rule string
		var advice string
		var effort int
		var category string
		var criticality string
		var ext string
		var pattern string
		var value string
		var readiness int

		for rows.Next() {
			err = rows.Scan(&runId, &application, &filename, &fqn, &line, &rule, &advice, &effort, &category, &criticality, &ext, &pattern, &value, &readiness)
			if err != nil {
				fmt.Print(err)
				os.Exit(1)
			}

			fileFinding := model.Finding{
				RunID:       runId,
				Filename:    filename,
				Fqn:         fqn,
				Ext:         ext,
				Category:    category,
				Pattern:     pattern,
				Value:       value,
				Effort:      effort,
				Rule:        rule,
				Advice:      advice,
				Readiness:   readiness,
				Criticality: criticality,
				Application: application,
				Line:        line,
			}

			fileFindings = append(fileFindings, fileFinding)
		}
		if util.CsvReportFileName != nil {
			csaService.generateCsvReport(fileFindings)
		}
		if util.HtmlReportFileName != nil {
			csaService.generateHtmlReport(fileFindings, run)
		}
	}

	for _, app := range run.Applications {
		line := []string{app.Name, fmt.Sprint(len(app.Files)), fmt.Sprint(len(app.IgnoredFiles)),
			fmt.Sprint(app.SlocCnt), fmt.Sprint(app.CIFindings), app.ScoringModel, fmt.Sprintf("%2.2f", app.Score), app.Recommendation}
		data = append(data, line)

		if *util.DisplayIgnoredFiles {
			fmt.Printf("\n\n---- App [%s] Ignored Files ----\n", app.Name)
			for _, file := range app.IgnoredFiles {
				fmt.Printf("\t%s\t\t%s\n", file.Name, file.FQN)
			}
		}
	}

	if *util.DisplayIgnoredFiles {
		fmt.Printf("\n\n--- END IGNORED FILES ---\n\n")
	}

	csaService.reportService.DisplayReport(headers, data, "CSA Results", false)

}

func (csaService *CsaService) generateCsvReport(findings []model.Finding) {
	var reportFile = *util.ReportDir + string(os.PathSeparator) + *util.CsvReportFileName

	file, err := os.Create(reportFile)
	if err != nil {
		fmt.Printf("failed creating file: %s", err)
	}
	defer file.Close()
	_, err1 := file.WriteString("application,filename,fqn,line,rule,advice,effort,category,criticality,ext,pattern,value,readiness,level\n")
	if err1 != nil {
		fmt.Printf("failed write to file file: %s", err1)
	}
	for _, finding := range findings {

		finding.Value = strings.Replace(finding.Value, ",", "", -2)
		finding.Value = strings.Replace(finding.Value, "\n", "", -2)
		finding.Value = strings.Replace(finding.Value, "\r", "", -2)

		line := fmt.Sprintf("\"%s\",\"%s\",\"%s\",%d,\"%s\",\"%s\",%d,\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\",\"%s\"\n", finding.Application, finding.Filename, finding.Fqn, finding.Line, finding.Rule, finding.Advice, finding.Effort, finding.Category, finding.Criticality, finding.Ext, finding.Pattern, finding.Value, finding.Readiness, GetLevelForScore(finding.Effort))
		_, err2 := file.WriteString(line)
		if err2 != nil {
			fmt.Printf("failed write to file file: %s", err2)
		}
	}

	fmt.Printf("\n\nWrote Csv Report file %s\n", reportFile)
}

func (csaService *CsaService) generateHtmlReport(findings []model.Finding, run *model.Run) {

	var fileContent = csaService.reportService.GetTemplateByName("HtmlReportTemplate.html")
	var reportFile = *util.ReportDir + string(os.PathSeparator) + *util.HtmlReportFileName

	sort.Slice(findings[:], func(i, j int) bool {
		return findings[i].Effort > findings[j].Effort
	})

	templateContentHtml := fileContent
	file, err := os.Create(reportFile)
	if err != nil {
		fmt.Printf("failed creating file: %s", err)
	}
	defer file.Close()

	metadataHtml := "<li>Target: " + run.Target + "</li>"
	metadataHtml += "<li>Created Time: " + run.CreatedAt.Format(time.ANSIC) + "</li>"
	metadataHtml += "<li>#Findings: " + strconv.Itoa(run.Findings) + "</li>"
	var b bytes.Buffer
	fmt.Printf("Findings [%d]", len(findings))
	for _, finding := range findings {
		b.WriteString("<tr>")
		b.WriteString("<td class=\"small\">" + strconv.FormatUint(uint64(finding.RunID), 10) + "</td>")
		b.WriteString("<td>" + finding.Application + "</td>")
		b.WriteString("<td>" + finding.Category + "</td>")
		b.WriteString("<td>" + finding.Rule + "</td>")
		b.WriteString("<td>" + finding.Fqn + "</td>")
		b.WriteString("<td class=\"small\">" + strconv.Itoa(finding.Line) + "</td>")
		b.WriteString("<td>" + finding.Value + "</td>")
		b.WriteString("<td class=\"small\">" + GetLevelForScore(finding.Effort) + "</td>")
		b.WriteString("<td class=\"small\">" + strconv.Itoa(finding.Effort) + "</td>")
		b.WriteString("<td>" + finding.Advice + "</td>")
		b.WriteString("</tr>")
	}

	templateContentHtml = strings.Replace(templateContentHtml, "${metadata}", metadataHtml, -1)
	templateContentHtml = strings.Replace(templateContentHtml, "${table}", b.String(), -1)

	data := []byte(templateContentHtml)
	err1 := ioutil.WriteFile(reportFile, data, 0)
	if err1 != nil {
		fmt.Printf("failed write to file file: %s", err1)
	}

	fmt.Printf("\n\nWrote Html Report file %s\n", reportFile)
}

func (csaService *CsaService) gatherFiles(run *model.Run) {

	runConfig := model.NewRunConfig(run, csaService.fileUtil)
	runConfig.UnMarshall()

	if *util.WriteConfigsOnly {
		return
	}

	if len(runConfig.Applications) > 0 {
		run.SetAlias(runConfig.Alias)
		run.StartActivity("gathering")
		runConfig.Populate()
		if !*util.Xtract {
			fmt.Printf("Found [%d] Applications...\n", len(runConfig.Applications))
		}

		if !*util.Xtract {
			run.StopActivityLF("gathering", "Gathering Files...done!\n", false, true)
			fmt.Print("\nApp/File Details:\n\n")
		} else {
			run.StopActivityLF("", "\n", false, true)
			//screen.Clear()
		}
		csaService.UpdateRunWithApplications(run, runConfig)
		fmt.Println("")
	}
}

func (csaService *CsaService) UpdateRunWithApplications(run *model.Run, rc *model.RunConfig) {
	var err error
	var filesCnt = 0

	longestName := 0
	for _, app := range rc.Applications {
		if len(app.Name) > longestName {
			longestName = len(app.Name)
		}
	}

	stdOutFmt := "\t%" + strconv.Itoa(longestName) + "s has [%d] files\n"
	stdOutFmtWError := "\t%" + strconv.Itoa(longestName) + "s has [%d] files. Error => %s\n"

	for i := range rc.Applications {
		newApp := model.NewApplication(rc.Applications[i])
		cnt := len(newApp.Files)
		newApp.FilesCnt = cnt
		filesCnt += cnt
		newApp.Rules, err = csaService.getRules(run, rc.Applications[i])
		if err != nil {
			util.TrackError("gathering", fmt.Errorf("error getting rules for app [%s]. details: %s\n", newApp.Name, err.Error()))
		} else {
			newApp.Model, err = csaService.getScoringModel(newApp.ScoringModel)
			if err != nil {
				util.TrackError("gathering", fmt.Errorf("error getting getting scoring model for app [%s]. details: %s\n", newApp.Name, err.Error()))
			}
		}

		run.AssociateApplication(newApp)
		//Write app msg for cli
		if err == nil {
			if !*util.Xtract {
				fmt.Printf(stdOutFmt, newApp.Name, cnt)
			}
		} else {
			fmt.Printf(stdOutFmtWError, newApp.Name, cnt, err.Error())
		}
	}

	run.Files = filesCnt
	if !*util.Xtract {
		fmt.Printf("\n**** Found [%d] total Files to Analyze ****\n", filesCnt)
	}
}

func (csaService *CsaService) startWorkers(run *model.Run) (saveWorkerCnt int, indexWorkerCnt int) {

	//Start Save Worker
	saveWorkerCnt = util.StartWorkers(csaService.saveFindingToDb, "findings-save-worker", run.Files, 50, *util.MaxSaveWorkers, csaService.saveChan, csaService.saveDone, csaService.indexChan, run)

	//Determined that bleve can only support a single write at a time so having a bunch of workers doesn't do a thing!
	if *util.TxtIndexingEnabled {
		indexWorkerCnt = util.StartWorkers(csaService.addFindingToTextIndex, "findings-index-worker", run.Files, run.Files, *util.MaxIndexWorkers, csaService.indexChan, csaService.indexDone, nil, run)
	}

	if *util.Verbose {
		fmt.Printf("[%d] Save Workers started. [%d] indexWorkers started.\n", saveWorkerCnt, indexWorkerCnt)
	}

	return
}

func (csaService *CsaService) waitForSavingAndIndexingToComplete(run *model.Run, saveWorkerCnt int, indexWorkerCnt int) {

	//Wait for saving to be done
	for i := 0; i < (saveWorkerCnt); i++ {
		<-csaService.saveDone
	}

	csaService.savingDone = true
	close(csaService.saveDone)
	//Make sure we write the final 100%
	util.WriteLogWithToken("Saving", fmt.Sprintf("%2.f%%", float64(csaService.findingsSaved)/float64(run.Findings)*100), "Saving Done!\n")

	close(csaService.indexChan)

	msg := "done!"
	if util.ProcessHadErrors("Saving") {
		msg = "errors!"
	}

	run.StopActivityLF("saving", fmt.Sprintf("Saving...%s", msg), false, true)

	for i := 0; i < (indexWorkerCnt); i++ {
		<-csaService.indexDone
	}
	close(csaService.indexDone)

	if *util.TxtIndexingEnabled {
		if csaService.findingsSaved == csaService.findingsIndexed {
			util.WriteLogWithToken("Indexing", fmt.Sprintf("%2.f%%", float64(csaService.findingsIndexed)/float64(run.Findings)*100), "Indexing Done!\n")
			run.StopActivityLF("indexing", "Indexing...done!", false, true)
		} else {
			run.StopActivityLF("indexing", "Indexing...failed!", false, true)
		}
	}

}

func (csaService *CsaService) getScoringModel(modelName string) (*model.ScoringModel, error) {
	//Get scoring model for run!
	m, err := csaService.scoringRepository.GetModelByName(modelName)
	if err != nil {
		return nil, err
	}

	return m, nil
}

func closeFile(file *os.File) {
	err := file.Close()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failure closing file [%s] => %s", file.Name(), err.Error())
	}
}
