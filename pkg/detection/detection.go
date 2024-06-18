package detection

import (
	"crypto/x509"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	cmn "github.com/pzaino/thecrowler/pkg/common"
	ruleset "github.com/pzaino/thecrowler/pkg/ruleset"

	"github.com/tebeka/selenium"
)

const (
	errMatchingSignature = "matching signature: %v"
)

// detectionEntityDetails is used internally to represent the details of an entity detection
type detectionEntityDetails struct {
	entityType      string
	matchedPatterns []string
	confidence      float32
	pluginResult    map[string]interface{}
}

func (d detectionEntityDetails) IsEmpty() bool {
	return reflect.DeepEqual(d, detectionEntityDetails{})
}

func DetectTechnologies(dtCtx *DetectionContext) *map[string]DetectedEntity {
	cmn.DebugMsg(cmn.DbgLvlDebug, "Starting technologies detection...")

	// micro-signatures
	Patterns := dtCtx.RE.GetAllEnabledDetectionRules()
	if len(Patterns) == 0 {
		cmn.DebugMsg(cmn.DbgLvlDebug, "No detection rules enabled")
		return nil
	}

	// Initialize a slice to store the detected stuff
	detectedTech := make(map[string]detectionEntityDetails)

	var responseBody string
	if dtCtx.ResponseBody != nil {
		// Normalize the response body
		responseBody = strings.ToLower(strings.TrimSpace(*dtCtx.ResponseBody))
	} else if dtCtx.WD != nil {
		// Get the page source from the WebDriver
		pageSource, err := (*dtCtx.WD).PageSource()
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, "getting page source: %s", err)
		} else {
			// Normalize the page source
			responseBody = strings.ToLower(strings.TrimSpace(pageSource))
		}
	} else {
		cmn.DebugMsg(cmn.DbgLvlError, "no response body provided for detection and pointer to the VDI is nil")
	}

	// Iterate through all the header tags and check for CMS signatures
	if dtCtx.Header != nil {
		const (
			hostHeader = "Host-Header"
			xGenerator = "X-Generator"
		)
		for headerTag := range *dtCtx.Header {
			// Get the HTTP header fields for the specific tag
			var Signatures map[string]map[string]ruleset.HTTPHeaderField
			if headerTag == hostHeader {
				Signatures = ruleset.GetAllHTTPHeaderFieldsMap(&Patterns)
			} else {
				Signatures = ruleset.GetHTTPHeaderFieldsMapByKey(&Patterns, headerTag)
			}
			if (Signatures != nil) && len(Signatures) > 0 {
				detectTechByTag(dtCtx.Header, headerTag, &Signatures, &detectedTech)
			}
		}
		// Some extra tags that may help:
		if dtCtx.Header.Get(xGenerator) != "" {
			entity := detectionEntityDetails{
				entityType:      "header_field",
				confidence:      10,
				matchedPatterns: []string{xGenerator},
			}
			detectedTech[xGenerator] = entity
		}
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug, "Skipping header detection because the header is nil")
	}

	// Try to detect technologies using URL's micro-signatures (e.g., /wp-content/)
	if dtCtx.TargetURL != "" {
		URLSignatures := ruleset.GetAllURLMicroSignaturesMap(&Patterns)
		detectTechByURL(dtCtx.TargetURL, &URLSignatures, &detectedTech)
		URLSignatures = nil
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug, "Skipping URL detection because the target URL is empty")
	}

	if responseBody != "" {
		// Try to detect technologies using meta tags
		MetaTagsSignatures := ruleset.GetAllMetaTagsMap(&Patterns)
		detectTechByMetaTags(responseBody, &MetaTagsSignatures, &detectedTech)
		MetaTagsSignatures = nil

		// Check the response body for Technologies signatures
		Signatures := ruleset.GetAllPageContentPatternsMap(&Patterns)
		detectTechnologiesByKeyword(responseBody, &Signatures, &detectedTech)
		Signatures = nil
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug, "Skipping HTML detection because the response body is empty")
	}

	// Try to detect technologies using plugins
	if dtCtx.WD != nil {
		Plugins := ruleset.GetAllPluginCallsMap(&Patterns)
		if len(Plugins) > 0 {
			detectTechnologiesWithPlugins(dtCtx.WD, dtCtx.RE, &Plugins, &detectedTech)
		} else {
			cmn.DebugMsg(cmn.DbgLvlDebug, "No detection rules requiring plugins")
		}
		Plugins = nil
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug, "Skipping plugin detection because the WebDriver is nil")
	}

	// Check for SSL/TLS technologies
	if dtCtx.HSSLInfo != nil {
		sslSignatures := ruleset.GetAllSSLSignaturesMap(&Patterns)
		detectTechBySSL(dtCtx.HSSLInfo, &sslSignatures, &detectedTech)
		sslSignatures = nil
	} else {
		cmn.DebugMsg(cmn.DbgLvlDebug, "Skipping SSL detection because the SSLInfo is nil")
	}

	// Process implied technologies
	if len(detectedTech) > 0 {
		processImpliedTechnologies(&detectedTech, &Patterns)
	}

	// Transform the detectedTech map into a map of strings
	detectedTechStr := make(map[string]DetectedEntity)
	if len(detectedTech) == 0 {
		cmn.DebugMsg(cmn.DbgLvlDebug, "No technologies detected")
		return &detectedTechStr
	}

	// Iterate through the detected technologies and calculate the confidence
	for k, v := range detectedTech {
		// calculate "confidence" based on the value of x
		if !v.IsEmpty() {
			x := v.confidence
			c := calculateConfidence(x, dtCtx.RE.DetectionConfig.NoiseThreshold, dtCtx.RE.DetectionConfig.MaybeThreshold, dtCtx.RE.DetectionConfig.DetectedThreshold)
			if c <= 10 {
				continue
			}
			v.confidence = c
			if x < 0 {
				// If x is negative, then the analysis was on the ABSENCE of a technology
				// In this case we add a special prefix to the entity name
				k = "no_" + k
			}
			entity := DetectedEntity{
				EntityName:      k,
				EntityType:      v.entityType,
				Confidence:      v.confidence,
				MatchedPatterns: v.matchedPatterns,
			}
			detectedTechStr[k] = entity
		}
	}

	cmn.DebugMsg(cmn.DbgLvlDebug1, "Detected entities: %v", detectedTechStr)
	return &detectedTechStr
}

func processImpliedTechnologies(detectedTech *map[string]detectionEntityDetails, patterns *[]ruleset.DetectionRule) {
	for tech, details := range *detectedTech {
		for _, rule := range *patterns {
			if rule.ObjectName == tech {
				for _, impliedTech := range rule.GetImplies() {
					if _, alreadyDetected := (*detectedTech)[impliedTech]; !alreadyDetected {
						(*detectedTech)[impliedTech] = detectionEntityDetails{
							entityType:      "implied",
							confidence:      details.confidence,
							matchedPatterns: []string{"implied by " + tech},
						}
					}
				}
			}
		}
	}
}

func calculateConfidence(x, Noise, Maybe, Detected float32) float32 {
	// Confidence calculation based on the value of x
	if x < 0 {
		// If x is negative, then the analysis was on the ABSENCE of a technology
		// In this case, we want to calculate the confidence based on the absence of the technology
		// i.e. the further x is to 0, the higher the confidence
		return 100 - min(100, -x)
	} else if x < Noise {
		return (x / Noise) * 10 // Maps [0, Noise) to [0%, 10%]
	} else if x < Maybe {
		return 10 + ((x-Noise)/(Maybe-Noise))*30 // Maps [Noise, Maybe) to [10%, 40%]
	} else if x < Detected {
		return 40 + ((x-Maybe)/(Detected-Maybe))*60 // Maps [Maybe, Detected) to [40%, 100%]
	} else {
		// Maps [Detected, ∞) to [40%, 100%]
		// i.e. this ensures that confidence doesn't exceed 100%
		return min(100, 40+((x-Detected)/(Detected-Maybe))*60)
	}
}

func detectTechBySSL(sslInfo *SSLInfo, sslSignatures *map[string][]ruleset.SSLSignature, detectedTech *map[string]detectionEntityDetails) {
	for ObjName := range *sslSignatures {
		for _, signature := range (*sslSignatures)[ObjName] {
			detectSSLTechBySignatureValue(sslInfo.CertChain, signature, detectedTech, ObjName)
		}
	}
}

func detectSSLTechBySignatureValue(certChain []*x509.Certificate, signature ruleset.SSLSignature, detectedTech *map[string]detectionEntityDetails, ObjName string) {
	const (
		detectionType = "ssl_certificate"
	)
	for _, cert := range certChain {
		// Get Certificate field based on the signature key
		certField, err := getCertificateField(cert, signature.Key)
		if err != nil {
			continue
		} else {
			for _, signatureValue := range signature.Value {
				matched, err := regexp.MatchString(signatureValue, certField)
				if err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, errMatchingSignature, err)
				} else if matched {
					//if strings.Contains(certField, signatureValue) {
					updateDetectedTech(detectedTech, ObjName, signature.Confidence, signatureValue)
					updateDetectedType(detectedTech, ObjName, detectionType)
				}
			}
		}
	}
}

func getCertificateField(cert *x509.Certificate, key string) (string, error) {
	sValue := reflect.ValueOf(cert.Subject)
	sType := sValue.Type()
	for i := 0; i < sValue.NumField(); i++ {
		if sType.Field(i).Name == key {
			return sValue.Field(i).String(), nil
		}
	}
	return "", fmt.Errorf("field not found: %s", key)
}

func detectTechnologiesByKeyword(responseBody string, signatures *map[string][]ruleset.PageContentSignature, detectedTech *map[string]detectionEntityDetails) {
	// Create a new document from the HTML string
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(responseBody))
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "loading HTML: %s", err)
		return
	}
	// Iterate through all the signatures and check for possible technologies
	for sig := range *signatures {
		item := (*signatures)[sig]
		for _, signature := range item {
			detectTechBySignature(responseBody, doc, signature, sig, detectedTech)
		}
	}
}

func detectTechBySignature(responseBody string, doc *goquery.Document, signature ruleset.PageContentSignature, sig string, detectedTech *map[string]detectionEntityDetails) {
	if signature.Key == "*" {
		detectTechBySignatureValue(responseBody, signature.Signature, sig, detectedTech, signature.Confidence)
	} else {
		// prepare the signature key
		key := strings.ToLower(strings.TrimSpace(signature.Key))
		doc.Find(key).Each(func(index int, htmlItem *goquery.Selection) {
			var text1 string
			var text2 string
			var attrExists bool
			if (signature.Attribute != "") && (signature.Attribute != "text") {
				text1, attrExists = htmlItem.Attr(strings.ToLower(strings.TrimSpace(signature.Attribute)))
			}
			text2 = htmlItem.Text()
			if attrExists {
				detectTechBySignatureValue(text1, signature.Signature, sig, detectedTech, signature.Confidence)
			}
			if len(signature.Text) > 0 {
				detectTechBySignatureValue(text2, signature.Text, sig, detectedTech, signature.Confidence)
			}
		})
	}
}

func detectTechBySignatureValue(text string, signatures []string, sig string, detectedTech *map[string]detectionEntityDetails, confidence float32) {
	for _, sigValue := range signatures {
		if sigValue != "" {
			detectTechBySignatureValueHelper(text, sigValue, sig, detectedTech, confidence)
		}
	}
}

func detectTechBySignatureValueHelper(text string, sigValue string, sig string, detectedTech *map[string]detectionEntityDetails, confidence float32) {
	const detectionType = "html"
	if sigValue != "*" {
		matched, err := regexp.MatchString(sigValue, text)
		if err != nil {
			cmn.DebugMsg(cmn.DbgLvlError, errMatchingSignature, err)
		} else if matched {
			updateDetectedTech(detectedTech, sig, confidence, sigValue)
		}
	} else {
		// Just call updateDetectedTech if the signature is "*"
		updateDetectedTech(detectedTech, sig, confidence, "*")
	}
	updateDetectedType(detectedTech, sig, detectionType)
}

func updateDetectedTech(detectedTech *map[string]detectionEntityDetails, sig string, confidence float32, matchedSig string) {
	entity, ok := (*detectedTech)[sig]
	if ok {
		// If the entry exists, update its confidence and matched patterns
		entity.confidence += confidence
	} else {
		// Initialize a new entity if the entry doesn't exist
		entity.confidence = confidence
		entity.matchedPatterns = make([]string, 0)
	}
	// Append the pattern if it's not already added
	if !cmn.SliceContains(entity.matchedPatterns, matchedSig) {
		entity.matchedPatterns = append(entity.matchedPatterns, matchedSig)
	}

	// Save the updated entity back to the map
	(*detectedTech)[sig] = entity
}

func updateDetectedTechCustom(detectedTech *map[string]detectionEntityDetails, sig string, confidence float32, matchedSig string, custom string) {
	entity, ok := (*detectedTech)[sig]
	if ok {
		// If the entry exists, update its confidence and matched patterns
		entity.confidence += confidence
		// if custom is not empty, transform it to a JSON object
		customJSON := make(map[string]interface{})
		var err error
		if custom != "" {
			customJSON, err = cmn.JSONStrToMap(custom)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "parsing plugin custom JSON result: %s", err)
			}
		}
		entity.pluginResult = customJSON
	} else {
		// Initialize a new entity if the entry doesn't exist
		entity.confidence = confidence
		entity.matchedPatterns = make([]string, 0)
		// if custom is not empty, transform it to a JSON object
		customJSON := make(map[string]interface{})
		var err error
		if custom != "" {
			customJSON, err = cmn.JSONStrToMap(custom)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "parsing plugin custom JSON result: %s", err)
			}
		}
		entity.pluginResult = customJSON
	}
	// Append the pattern if it's not already added
	if !cmn.SliceContains(entity.matchedPatterns, matchedSig) {
		entity.matchedPatterns = append(entity.matchedPatterns, matchedSig)
	}

	// Save the updated entity back to the map
	(*detectedTech)[sig] = entity
}

func updateDetectedType(detectedTech *map[string]detectionEntityDetails, sig string, detectionType string) {
	entity := (*detectedTech)[sig]
	if entity.confidence != 0 {
		if entity.entityType == "" {
			entity.entityType = detectionType
		} else {
			if !strings.Contains(entity.entityType, detectionType) {
				entity.entityType += "," + detectionType
			}
		}
		(*detectedTech)[sig] = entity
	}
}

func detectTechByTag(header *http.Header, tagName string, detectRules *map[string]map[string]ruleset.HTTPHeaderField, detectedTech *map[string]detectionEntityDetails) {
	hh := (*header)[tagName] // get the header value (header tag name is case sensitive)
	tagName = strings.ToLower(tagName)
	if len(hh) != 0 {
		for _, tag := range hh {
			tag = strings.ToLower(tag)
			detectTechByTagHelper(tagName, tag, detectRules, detectedTech)
		}
	}
}

func detectTechByTagHelper(tagName string, tag string, detectRules *map[string]map[string]ruleset.HTTPHeaderField, detectedTech *map[string]detectionEntityDetails) {
	const (
		detectionType = "http_header"
	)
	for ObjName := range *detectRules {
		item := (*detectRules)[ObjName]
		for _, signature := range item[tagName].Value {
			if signature == "" {
				continue
			}
			if signature == "!*" {
				// "!*" This means check if the Signature Key is not present in the header.
				// Usually used for negative detection of headers like Content-Security-Policy
				// to identify if a site is secure or not.
				if !strings.Contains(tag, item[tagName].Key) {
					updateDetectedTech(detectedTech, ObjName, -item[tagName].Confidence, item[tagName].Key)
					continue
				}
			} else if signature != "*" {
				matched, err := regexp.MatchString(signature, tag)
				if err != nil {
					cmn.DebugMsg(cmn.DbgLvlError, errMatchingSignature, err)
					continue
				}
				if matched {
					updateDetectedTech(detectedTech, ObjName, item[tagName].Confidence, signature)
				}
			} else {
				updateDetectedTech(detectedTech, ObjName, item[tagName].Confidence, "*")
			}
			updateDetectedType(detectedTech, ObjName, detectionType)
		}
	}
}

func detectTechByMetaTags(responseBody string, signatures *map[string][]ruleset.MetaTag, detectedTech *map[string]detectionEntityDetails) {
	// Create a new document from the HTML string
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(responseBody))
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "loading HTML: %s", err)
		return
	}
	const detectionType = "meta_tags"
	// Iterate through all the meta tags and check for possible technologies
	for ObjName := range *signatures {
		for _, signature := range (*signatures)[ObjName] {
			doc.Find("meta").Each(func(index int, htmlItem *goquery.Selection) {
				if strings.EqualFold(htmlItem.AttrOr("name", ""), strings.TrimSpace(signature.Name)) {
					text, contExists := htmlItem.Attr("content")
					if contExists && signature.Content != "" {
						text = strings.ToLower(text)
						matched, err := regexp.MatchString(signature.Content, text)
						if err != nil {
							cmn.DebugMsg(cmn.DbgLvlError, errMatchingSignature, err)
						} else if matched {
							updateDetectedTech(detectedTech, ObjName, signature.Confidence, signature.Content)
						}
					}
					updateDetectedType(detectedTech, ObjName, detectionType)
				}
			})
		}
	}
}

// detectTechnologiesWithPlugins runs plugins in the browser and collects the results
// to detect technologies
func detectTechnologiesWithPlugins(wd *selenium.WebDriver, re *ruleset.RuleEngine, plugins *map[string][]ruleset.PluginCall, detectedTech *map[string]detectionEntityDetails) {
	//const detectionType = "plugin"
	// Iterate through all the plugins and check for possible technologies
	for ObjName := range *plugins {
		cmn.DebugMsg(cmn.DbgLvlDebug3, "Running plugins for: %s", ObjName)
		for _, pluginCall := range (*plugins)[ObjName] {
			cmn.DebugMsg(cmn.DbgLvlDebug5, "Plugin: %s", pluginCall.PluginName)
			// Retrieve the plugin from the Plugins table
			plugin, exists := re.JSPlugins.GetPlugin(pluginCall.PluginName)
			if !exists {
				cmn.DebugMsg(cmn.DbgLvlDebug3, "plugin not found: %s", pluginCall.PluginName)
				continue
			}
			// Get the plugin arguments
			var args []ruleset.PluginParams
			var jsArgs []interface{}
			var confidence float32
			if pluginCall.PluginArgs != nil {
				args = pluginCall.PluginArgs
				// Search for an arg called confidence
				for _, arg := range args {
					jsArgs = append(jsArgs, arg.ArgValue)
					if strings.ToLower(strings.TrimSpace(arg.ArgName)) == "confidence" {
						confidence = cmn.StringToFloat32(arg.ArgValue)
					}
				}
			}
			if confidence == 0 {
				confidence = 10
			}
			// Run the plugin
			cmn.DebugMsg(cmn.DbgLvlDebug5, "Executing Plugin: %s", pluginCall.PluginName)
			result, err := (*wd).ExecuteScript(plugin.String(), jsArgs)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, "running plugin: %s", err)
				continue
			}
			cmn.DebugMsg(cmn.DbgLvlDebug3, "Plugin execution result: %v", result)
			if result == nil {
				continue
			}
			// Convert result to a string
			resultStr, ok := result.(string)
			if !ok || resultStr == "" ||
				resultStr == "null" ||
				resultStr == "undefined" ||
				resultStr == "{}" ||
				resultStr == "[]" ||
				resultStr == "false" {
				// discard all empty results
				continue
			}
			// Check if resultStr is a valid JSON object
			if cmn.IsJSON(resultStr) {
				// Add the plugin result as PluginResult
				updateDetectedTechCustom(detectedTech, ObjName, confidence, pluginCall.PluginName, resultStr)
			} else {
				// log that the result is not a valid JSON object
				cmn.DebugMsg(cmn.DbgLvlDebug3, "Plugin result is not a valid JSON object: %s", resultStr)
			}
		}
	}
}

func detectTechByURL(url string, URLSignatures *map[string][]ruleset.URLMicroSignature, detectedTech *map[string]detectionEntityDetails) {
	for ObjName := range *URLSignatures {
		for _, signature := range (*URLSignatures)[ObjName] {
			matched, err := regexp.MatchString(signature.Signature, url)
			if err != nil {
				cmn.DebugMsg(cmn.DbgLvlError, errMatchingSignature, err)
				continue
			}
			if matched {
				updateDetectedTech(detectedTech, ObjName, signature.Confidence, signature.Signature)
				updateDetectedType(detectedTech, ObjName, "url")
			}
		}
	}
}
