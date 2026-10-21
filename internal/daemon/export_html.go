package daemon

import (
	v1 "github.com/octacian/backlot/api/v1"
	"html/template"
	"net/url"
	"path"
	"sort"
	"strings"
)

var exportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{"primaryEvidence": primaryExportEvidence, "hasArtifacts": func(files []v1.ExportEvidence) bool {
	for _, file := range files {
		if file.Kind == "artifact" {
			return true
		}
	}
	return false
}, "hasHTML": func(files []v1.ExportEvidence) bool {
	for _, file := range files {
		if strings.HasSuffix(strings.ToLower(file.Path), ".html") {
			return true
		}
	}
	return false
}, "hasTraces": func(files []v1.ExportEvidence) bool {
	for _, file := range files {
		if strings.HasSuffix(strings.ToLower(file.Path), "trace.zip") {
			return true
		}
	}
	return false
}, "evidenceURL": func(path string) string { return (&url.URL{Path: path}).String() }}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; base-uri 'none'; form-action 'none'"><title>Backlot · {{.Source.Scene}} · {{.Result.Status}}</title><style>
:root{color-scheme:light dark;--paper:#f5f3ed;--ink:#202d2b;--muted:#53625e;--line:#d3d9ce;--card:#fffef9;--accent:#165e4b}*{box-sizing:border-box}body{margin:0;background:var(--paper);color:var(--ink);font:16px/1.6 system-ui,sans-serif}main{max-width:1080px;margin:auto;padding:44px 28px 70px}a{color:var(--accent);overflow-wrap:anywhere}a:focus-visible{outline:3px solid #db8b22;outline-offset:4px}header{border-top:5px solid var(--accent);padding-top:22px;margin-bottom:28px}.brand{letter-spacing:.16em;font-size:12px;font-weight:750;text-transform:uppercase;color:var(--muted)}h1{font-size:clamp(30px,6vw,48px);line-height:1.15;margin:18px 0 10px;overflow-wrap:anywhere}h2{font-size:21px;margin-top:0}p{margin:8px 0 16px}.status{display:inline-block;background:#e5eee3;padding:4px 12px;border-radius:4px;font-weight:700}.status.failed{background:#fce5df;color:#8b251c}.status.cancelled,.status.interrupted,.status.stopped{background:#f5e9ce;color:#704700}.partial{color:#815400}.grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px;margin:24px 0}section,.metric{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:22px;margin-bottom:20px}.metric{margin:0}.label,dt{font-size:12px;text-transform:uppercase;letter-spacing:.08em;color:var(--muted);font-weight:700}.value{font-size:24px;font-weight:650;margin-top:6px}code{font:13px/1.5 ui-monospace,monospace;overflow-wrap:anywhere}dl{display:grid;grid-template-columns:150px minmax(0,1fr);gap:9px 20px}dd{margin:0;overflow-wrap:anywhere}.note{border-left:4px solid #c88820;padding-left:16px}.files{list-style:none;padding:0}.files li{border-top:1px solid var(--line);padding:14px 0}.meta{font-size:13px;color:var(--muted)}pre{white-space:pre-wrap;overflow-wrap:anywhere;font:14px/1.6 ui-monospace,monospace}.fail{color:#a43325}footer{font-size:13px;color:var(--muted)}@media(max-width:650px){main{padding:24px 16px 44px}.grid{grid-template-columns:1fr}dl{grid-template-columns:1fr;gap:4px}dd{margin-bottom:12px}section{padding:18px}}@media(prefers-color-scheme:dark){:root{--paper:#16201e;--ink:#e6e9e2;--muted:#b4c0b7;--line:#43514a;--card:#202b27;--accent:#a7d7ba}.status{background:#344d3b}.fail{color:#ffb2a5}.status.failed{background:#5a2e27;color:#ffd5cb}.status.cancelled,.status.interrupted,.status.stopped{background:#51462b;color:#ffe2a3}.partial{color:#ffe2a3}}
</style></head><body><main><header><div class="brand">Backlot / retained run report</div><h1>{{.Source.Project}} <span aria-hidden="true">/</span> {{.Source.Scene}}</h1><span class="status {{.Result.Status}}">{{.Result.Status}}</span><p>Portable evidence for one recorded execution attempt.</p><a href="report.json">Read the versioned agent report (JSON)</a></header>
<div class="grid"><div class="metric"><div class="label">Terminal exit</div><div class="value">{{if .Result.TerminalExitCode}}{{.Result.TerminalExitCode}}{{else}}Unavailable{{end}}</div></div><div class="metric"><div class="label">Cancellation</div><div class="value">{{if .Result.Cancelled}}Cancelled{{else}}Not cancelled{{end}}</div></div><div class="metric"><div class="label">Evidence</div><div class="value {{if not .Complete}}partial{{end}}">{{if .Complete}}Complete{{else}}Partial{{end}}</div></div></div>
{{if not .Complete}}<section class="note"><h2>Incomplete evidence</h2><p>This bundle was explicitly exported with partial evidence. Missing bytes have not been reconstructed.</p><ul>{{range .Omissions}}<li>{{.}}</li>{{end}}</ul></section>{{end}}
<section><h2>Outcome and diagnostics</h2>{{range .Result.NativeCleanup}}<h3>Native cleanup: {{.Component}}</h3><p>{{.Confidence}} ({{.Reason}}): {{.Detail}}</p>{{end}}{{if .Result.Failure}}<h3>Runtime failure</h3><pre class="fail">{{.Result.Failure}}</pre>{{end}}{{if .Result.CollectionFailure}}<h3>Collection failure</h3><pre class="fail">{{.Result.CollectionFailure}}</pre>{{end}}{{if .Result.CleanupFailure}}<h3>Cleanup failure</h3><pre class="fail">{{.Result.CleanupFailure}}</pre>{{end}}{{if and (not .Result.Failure) (not .Result.CollectionFailure) (not .Result.CleanupFailure)}}<p>No orchestration, collection, or cleanup failures were recorded. The terminal exit above is the original application result; a nonzero exit means the application failed.</p>{{end}}
{{if .Result.Components}}<ul>{{range .Result.Components}}<li><strong>{{.Name}}</strong> · {{.Status}}{{if .ExitCode}} · exit {{.ExitCode}}{{end}}</li>{{end}}</ul>{{else}}<p>No component results were recorded.</p>{{end}}</section>
<section><h2>Application logs</h2>{{if .Logs.Available}}<details><summary>Show embedded logs ({{.Logs.ShownRecords}} of {{.Logs.TotalRecords}} records)</summary><p class="meta">Standalone escaped text preview, up to 256 KiB including timestamp/component/stream context. Binary chunks are labelled and retain original base64.</p>{{if .Logs.OmittedBytes}}<p class="note">Head and tail excerpts are shown. {{.Logs.OmittedBytes}} of {{.Logs.TotalBytes}} formatted log bytes are omitted. The complete original records remain in <a href="logs.ndjson">logs.ndjson</a>.</p>{{else}}<p><a href="logs.ndjson">Complete original NDJSON logs</a></p>{{end}}<pre>{{.Logs.Head}}</pre>{{if .Logs.Tail}}<p class="label">Tail excerpt · intervening content omitted</p><pre>{{.Logs.Tail}}</pre>{{end}}{{if not .Logs.TotalRecords}}<p>No log records were captured for this attempt.</p>{{end}}</details>{{else}}<p>No retained log bytes are available in this bundle.</p>{{end}}</section>
<section><h2>Execution identity</h2><dl><dt>Instance</dt><dd><code>{{.Result.InstanceID}}</code></dd><dt>Attempt</dt><dd><code>{{.Result.Attempt}}</code> · latest settled attempt</dd><dt>Created</dt><dd>{{.CreatedAt}}</dd><dt>Completed</dt><dd>{{if .CompletedAt}}{{.CompletedAt}}{{else}}Unknown{{end}}</dd><dt>Exported</dt><dd>{{.ExportedAt}}</dd><dt>Launch Git commit</dt><dd><code>{{if .Source.GitHead}}{{.Source.GitHead}}{{else}}Unknown{{end}}</code></dd><dt>Manifest digest</dt><dd><code>{{.Source.ManifestDigest}}</code></dd></dl><p class="meta">Working tree changes are not captured by this report.</p></section>
<section><h2>Retained evidence</h2><p>All links are relative to this bundle. Keep the entire directory together when moving or uploading it.</p>{{if hasArtifacts .Evidence}}<p class="note">Artifacts are <strong>application-provided content</strong>. {{if hasHTML .Evidence}}Their own HTML pages may execute application scripts. Open an HTML report with its retained assets; some reports require a local static file server. {{end}}{{if hasTraces .Evidence}}For application-produced Playwright trace ZIPs, use <code>npx playwright show-trace PATH.zip</code> using a compatible installed Playwright version. {{end}}</p>{{end}}<p>NDJSON logs contain one JSON record per line; binary chunks use base64.</p><h3>Reports, logs and media</h3><ul class="files">{{range primaryEvidence .Evidence}}<li><a href="{{evidenceURL .Path}}">{{.Path}}</a><div class="meta">{{.Kind}}{{if .Component}} · {{.Component}} / {{.Name}}{{end}} · {{.Bytes}} bytes</div></li>{{else}}<li>No report, log, media or archive files are available.</li>{{end}}</ul><details><summary>Complete file inventory ({{len .Evidence}} files)</summary><ul class="files">{{range .Evidence}}<li><a href="{{evidenceURL .Path}}">{{.Path}}</a><div class="meta">{{.Kind}}{{if .Component}} · {{.Component}} / {{.Name}}{{end}} · {{.Bytes}} bytes</div></li>{{else}}<li>No retained evidence files are available.</li>{{end}}</ul></details></section>
<footer>Schema {{.SchemaVersion}} · Export success describes this bundle’s publication. It does not change the recorded run outcome. Applications remain responsible for secrets printed in their logs and reports.</footer></main></body></html>`))

// Primary links are selected by generic file format, never by parsing an application's report.
func primaryExportEvidence(files []v1.ExportEvidence) []v1.ExportEvidence {
	var primary []v1.ExportEvidence
	for _, file := range files {
		switch strings.ToLower(path.Ext(file.Path)) {
		case ".html", ".htm", ".png", ".jpg", ".jpeg", ".webp", ".pdf", ".zip":
			primary = append(primary, file)
		default:
			if file.Kind == "logs" {
				primary = append(primary, file)
			}
		}
	}
	sort.SliceStable(primary, func(i, j int) bool { return primary[i].Kind == "logs" && primary[j].Kind != "logs" })
	return primary
}
