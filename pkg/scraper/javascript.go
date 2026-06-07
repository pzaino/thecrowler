package scraper

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
	vdi "github.com/pzaino/thecrowler/pkg/vdi"
)

// CollectedScript represents a JavaScript resource collected from a page.
type CollectedScript struct {
	ID           uint64   `json:"id"`
	ScriptType   string   `json:"script_type"`
	Original     string   `json:"original"`
	Script       string   `json:"script"`
	Errors       []string `json:"errors"`
	IsObfuscated bool     `json:"is_obfuscated"`
}

// JavaScriptRequest describes JavaScript collection from a WebDriver.
type JavaScriptRequest struct{ Driver *vdi.WebDriver }

// JavaScriptResult contains collected scripts.
type JavaScriptResult struct{ Scripts []CollectedScript }

// ExtractJavaScriptFiles collects inline and external scripts using only a WebDriver.
func ExtractJavaScriptFiles(req JavaScriptRequest) (JavaScriptResult, error) {
	if req.Driver == nil || *req.Driver == nil {
		return JavaScriptResult{}, fmt.Errorf("web driver is required")
	}
	const script = `
var scripts = document.getElementsByTagName('script');
var result = [];
for (var i = 0; i < scripts.length; i++) {
  if (scripts[i].src) {
    try {
      var xhr = new XMLHttpRequest();
      xhr.open('GET', scripts[i].src, false);
      xhr.send(null);
      if (xhr.status === 200) result.push({type: 'external', content: xhr.responseText});
      else result.push({type: 'external', content: 'Error: ' + xhr.statusText});
    } catch (e) { result.push({type: 'external', content: 'Error: ' + e.message}); }
  } else result.push({type: 'inline', content: scripts[i].innerHTML});
}
return result;`
	value, err := (*req.Driver).ExecuteScript(script, nil)
	if err != nil {
		return JavaScriptResult{}, fmt.Errorf("collect JavaScript files: %w", err)
	}
	contents, ok := value.([]interface{})
	if !ok {
		return JavaScriptResult{}, fmt.Errorf("expected script content to be a slice, got %T", value)
	}
	result := JavaScriptResult{Scripts: make([]CollectedScript, 0, len(contents))}
	for i, raw := range contents {
		entry, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		original, contentOK := entry["content"].(string)
		scriptType, typeOK := entry["type"].(string)
		if !contentOK || !typeOK {
			continue
		}
		obfuscated := DetectObfuscation(original)
		source := ""
		if obfuscated {
			source = DeobfuscateScript(original)
		}
		content := fmt.Sprintf("<!---- TheCRWOler: [Extracted script number: %d, of type: %s] //---->\n%s\n<!---- TheCRWOler: [End of extracted script number: %d] //---->", i, scriptType, source, i)
		result.Scripts = append(result.Scripts, CollectedScript{ID: uint64(i), ScriptType: scriptType, Original: original, Script: content, Errors: LintScript(content), IsObfuscated: obfuscated})
	}
	return result, nil
}

// DetectObfuscation reports whether common JavaScript obfuscation markers exist.
func DetectObfuscation(content string) bool {
	return strings.Contains(content, "eval(function(p,a,c,k,e,d)") || ContainsObfuscationPatterns(content)
}

// ContainsObfuscationPatterns reports whether encoded-string markers exist.
func ContainsObfuscationPatterns(content string) bool {
	patterns := []string{`\\x[0-9a-fA-F]{2}`, `\\u[0-9a-fA-F]{4}`, `String\.fromCharCode`, `unescape\(`}
	for _, pattern := range patterns {
		if regexp.MustCompile(pattern).MatchString(content) {
			return true
		}
	}
	return false
}

// DeobfuscateScript decodes hexadecimal and Unicode escape sequences.
func DeobfuscateScript(content string) string {
	unicodeEscapes := regexp.MustCompile(`\\u[0-9a-fA-F]{4}`)
	content = unicodeEscapes.ReplaceAllStringFunc(content, func(match string) string {
		value, _ := strconv.ParseInt(match[2:], 16, 32)
		return string(rune(value))
	})
	hexEscapes := regexp.MustCompile(`\\x[0-9a-fA-F]{2}`)
	return hexEscapes.ReplaceAllStringFunc(content, func(match string) string {
		value, _ := strconv.ParseInt(match[2:], 16, 32)
		return string(rune(value))
	})
}

// LintScript returns parser errors reported by esbuild.
func LintScript(content string) []string {
	transformed := api.Transform(content, api.TransformOptions{Loader: api.LoaderJS})
	errors := make([]string, 0, len(transformed.Errors))
	for _, lintErr := range transformed.Errors {
		errors = append(errors, lintErr.Text)
	}
	return errors
}
