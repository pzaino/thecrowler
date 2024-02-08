// Copyright 2023 Paolo Fabio Zaino
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package crawler implements the crawling logic of the application.
// It's responsible for crawling a website and extracting information from it.
package crawler

import (
	"regexp"
	"strings"
	"unicode"

	cmn "github.com/pzaino/thecrowler/pkg/common"

	"github.com/PuerkitoBio/goquery"
)

const (
	p string = ".,?!:;\"'()[]{}<>"
)

// Function that returns false if the keyword is
// an English language stop word, article, or preposition
// and true otherwise
func isKeyword(keyword string) bool {
	// List of English language stop words, articles, and prepositions
	stopWords := []string{"a", "about", "above", "across", "after", "afterwards", "again", "against",
		"all", "almost", "alone", "along", "already", "also", "although", "always", "am", "among",
		"amongst", "amongst", "amount", "an", "and", "another", "any", "anyhow", "anyone", "anything",
		"anyway", "anywhere", "are", "around", "as", "at", "back", "be", "became", "because", "become",
		"becomes", "becoming", "been", "before", "beforehand", "behind", "being", "below", "beside",
		"besides", "between", "beyond", "bill", "both", "bottom", "but", "by", "call", "can",
		"cannot", "can't", "co", "computer", "con", "could", "couldn't", "cry", "de", "describe",
		"detail", "do", "doesn't", "done", "down", "due", "during", "each", "eg", "eight", "either", "eleven", "else",
		"elsewhere", "empty", "enough", "etc", "even", "ever", "every", "everyone", "everything", "everywhere", "except", "few", "fifteen", "fifty",
		"fill", "find", "fire", "first", "five", "for", "former", "formerly", "forty", "found", "four",
		"from", "front", "full", "further", "get", "give", "go", "had", "has", "hasn't", "have",
		"he", "hence", "her", "here", "hereafter", "hereby", "herein", "hereupon", "hers", "herself",
		"him", "himself", "his", "how", "however", "hundred", "i", "ie", "if", "in", "inc", "indeed",
		"interest", "into", "is", "it", "it's", "itself", "keep", "last", "latter", "latterly", "least",
		"less", "ltd", "made", "many", "may", "me", "meanwhile", "might", "mill", "mine", "more",
		"moreover", "most", "mostly", "move", "much", "must", "my", "myse", "name", "namely",
		"neither", "never", "nevertheless", "next", "nine", "no", "nobody", "none", "noone", "nor",
		"not", "nothing", "now", "nowhere", "of", "off", "often", "on", "once", "one", "only",
		"onto", "or", "other", "others", "otherwise", "our", "ours", "ourselves", "out", "over",
		"own", "part", "per", "perhaps", "please", "put", "rather", "re", "same", "see", "seem",
		"seemed", "seeming", "seems", "serious", "several", "she", "should", "show", "side", "since",
		"sincere", "six", "sixty", "so", "some", "somehow", "someone", "something", "sometime",
		"sometimes", "somewhere", "still", "such", "system", "take", "ten", "than", "that", "the",
		"their", "them", "themselves", "then", "thence", "there", "thereafter", "thereby",
		"therefore", "therein", "thereupon", "these", "they", "thick", "thin", "third", "this",
		"those", "though", "three", "through", "throughout", "thru", "thus", "to", "together",
		"too", "top", "toward", "towards", "twelve", "twenty", "two", "un", "under", "until",
		"up", "upon", "us", "very", "via", "was", "we", "well", "were", "what", "whatever",
		"when", "whence", "whenever", "where", "whereafter", "whereas", "whereby", "wherein",
		"whereupon", "wherever", "whether", "which", "while", "whither", "who", "whoever", "whole",
		"whom", "whose", "why", "will", "with", "within", "without", "would", "yet", "you", "your",
		"yours", "yourself", "yourselves", "!", "@", "#", "$", "%", "^", "&", "*", "(", ")", "_", "+", "=", "-", "[", "]", "{", "}", "|", ";", ":", "'", "\"", ",", ".", "/", "<", ">", "?", "`", "~", "·", "！", "￥", "…", "（", "）", "—", "【", "】", "、", "；", "：", "‘", "’", "“", "”", "，", "。", "《", "》", "？", "·", "～", "1", "2", "3", "4", "5", "6", "7", "8", "9", "0", " ", "	", "\n", "\r", "\t", "　", " ", "	", "\n", "\r", "\t", "　", "||", "&&", "➤", "[];", "©"}

	// Convert the keyword to lowercase
	keyword = strings.ToLower(keyword) // Convert to lowercase

	// remove leading and trailing whitespace
	keyword = strings.TrimSpace(keyword)

	// remove trailing punctuation
	keyword = strings.TrimRight(keyword, p)

	// remove leading punctuation
	keyword = strings.TrimLeft(keyword, p)

	// basic checks:
	if len(keyword) < 3 {
		return false
	}

	// Check if the keyword is just a string of symbols
	if strings.Trim(keyword, ".,?!:;\"'()[]{}<>=+-/\\_") == "" {
		return false
	}

	// Check if the keyword contains /* */ or <!-- -->
	if keyword == "/*" || keyword == "*/" ||
		keyword == "<!--" || keyword == "-->" {
		return false
	}

	// Check if the keyword is in the stopWords list
	for _, word := range stopWords {
		if keyword == word {
			return false
		}
	}

	return true
}

func extractFromMetaTag(metaTags map[string]string, tagName string) []string {
	var keywords []string
	if content, ok := metaTags[tagName]; ok {
		for _, keyword := range strings.Split(content, ",") {
			trimmedKeyword := strings.TrimSpace(keyword)
			// Convert the keyword to lowercase
			trimmedKeyword = strings.ToLower(trimmedKeyword) // Convert to lowercase

			// remove trailing punctuation
			trimmedKeyword = strings.TrimRight(trimmedKeyword, p)

			// remove leading punctuation
			trimmedKeyword = strings.TrimLeft(trimmedKeyword, p)

			// remove leading and trailing whitespace
			trimmedKeyword = strings.TrimSpace(trimmedKeyword)

			if len(trimmedKeyword) > 45 {
				// Skip words that are too long
				continue
			}

			if trimmedKeyword != "" && isKeyword(trimmedKeyword) {
				keywords = append(keywords, trimmedKeyword)
			}
		}
	}
	return keywords
}

func extractContentKeywords(content string) []string {
	// Basic implementation: split words and filter
	// More advanced implementation might use NLP techniques
	var keywords []string
	words := strings.Fields(content) // Split into words
	for _, word := range words {
		trimmedWord := strings.TrimSpace(word)

		// Convert the keyword to lowercase
		trimmedWord = strings.ToLower(trimmedWord) // Convert to lowercase

		// remove trailing punctuation
		trimmedWord = strings.TrimRight(trimmedWord, p)

		// remove leading punctuation
		trimmedWord = strings.TrimLeft(trimmedWord, p)

		// remove leading and trailing whitespace
		trimmedWord = strings.TrimSpace(trimmedWord)

		if len(trimmedWord) > 45 {
			// Skip words that are too long
			continue
		}

		if trimmedWord != "" && isKeyword(trimmedWord) {
			keywords = append(keywords, trimmedWord)
		}
	}
	return keywords
}

func unique(strSlice []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range strSlice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

func extractKeywords(pageInfo PageInfo) []string {
	var keywords []string

	// Load the HTML document
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(pageInfo.BodyText))
	if err != nil {
		cmn.DebugMsg(cmn.DbgLvlError, "Error loading HTML content: %v", err)
		return nil
	}

	// Remove script, style tags, and other non-relevant elements
	doc.Find("script, style").Remove()

	content := normalizeText(doc.Text())

	// Extract from meta tags (keywords and description)
	keywords = append(keywords, extractFromMetaTag(pageInfo.MetaTags, "keywords")...)
	keywords = append(keywords, extractFromMetaTag(pageInfo.MetaTags, "description")...)

	// Extract from main content
	contentKeywords := extractContentKeywords(content)
	keywords = append(keywords, contentKeywords...)

	return unique(keywords) // Remove duplicates and return
}

func normalizeText(text string) string {
	// Remove all HTML tags
	re := regexp.MustCompile("<[^>]*>")
	text = re.ReplaceAllString(text, " ")

	// Remove inline JavaScript and CSS remnants
	// Adjust the regex as needed to catch any patterns of leftovers
	re = regexp.MustCompile(`(?i)<script.*?\/script>|<style.*?\/style>`)
	text = re.ReplaceAllString(text, " ")

	// Convert to lowercase
	text = strings.ToLower(text)

	// Remove punctuation and non-letter characters
	text = removePunctuation(text)

	// Normalize whitespace
	text = strings.Join(strings.Fields(text), " ")

	return text
}

func removePunctuation(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return -1
		}
		return r
	}, text)
}
