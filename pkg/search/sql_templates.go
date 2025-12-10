// Package search implements the search functionality for TheCrowler.
package search

// Base query used for SearchIndex search.
var sqlSearchIndexBody = `
SELECT DISTINCT
    si.title,
    si.page_url,
    si.summary,
    si.detected_type,
    si.detected_lang,
    wo.object_content AS content
FROM
    SearchIndex si
LEFT JOIN
    WebObjectsIndex woi ON si.index_id = woi.index_id
LEFT JOIN
    WebObjects wo ON woi.object_id = wo.object_id
LEFT JOIN
    KeywordIndex ki ON si.index_id = ki.index_id
LEFT JOIN
    Keywords k ON ki.keyword_id = k.keyword_id
WHERE
`

// Same as above but without returning content.
var sqlSearchIndexBodyNoContent = `
SELECT DISTINCT
    si.title,
    si.page_url,
    si.summary,
    si.detected_type,
    si.detected_lang,
    '' AS content
FROM
    SearchIndex si
LEFT JOIN
    WebObjectsIndex woi ON si.index_id = woi.index_id
LEFT JOIN
    WebObjects wo ON woi.object_id = wo.object_id
LEFT JOIN
    KeywordIndex ki ON si.index_id = ki.index_id
LEFT JOIN
    Keywords k ON ki.keyword_id = k.keyword_id
WHERE
`

var sqlScreenshotBody = `
SELECT DISTINCT
    s.screenshot_link,
    s.created_at,
    s.last_updated_at,
    s.type,
    s.format,
    s.width,
    s.height,
    s.byte_size
FROM
    Screenshots AS s
JOIN
    SearchIndex AS si ON s.index_id = si.index_id
LEFT JOIN
    KeywordIndex ki ON si.index_id = ki.index_id
LEFT JOIN
    Keywords k ON ki.keyword_id = k.keyword_id
WHERE
    s.screenshot_link != '' AND s.screenshot_link IS NOT NULL AND
`

var sqlWebObjectsBody = `
SELECT DISTINCT
    wo.object_link,
    wo.created_at,
    wo.last_updated_at,
    wo.object_type,
    wo.object_hash,
    wo.object_content,
    wo.object_html,
    wo.details
FROM
    WebObjects AS wo
JOIN
    WebObjectsIndex AS woi ON wo.object_id = woi.object_id
JOIN
    SearchIndex AS si ON woi.index_id = si.index_id
LEFT JOIN
    KeywordIndex ki ON si.index_id = ki.index_id
LEFT JOIN
    Keywords k ON ki.keyword_id = k.keyword_id
WHERE
    wo.object_link != '' AND wo.object_link IS NOT NULL AND
`

var sqlScrapedDataBody = `
SELECT DISTINCT
    ss.source_id,
    si.page_url AS url,
    sd.last_updated_at AS collected_at,
    sd.details->'scraped_data' AS scraped_data
FROM
    WebObjects AS sd
JOIN
    WebObjectsIndex AS woi ON sd.object_id = woi.object_id
JOIN
    SearchIndex AS si ON woi.index_id = si.index_id
LEFT JOIN
    KeywordIndex ki ON si.index_id = ki.index_id
LEFT JOIN
    Keywords k ON ki.keyword_id = k.keyword_id
LEFT JOIN
    SourceSearchIndex ss ON si.index_id = ss.index_id
WHERE
    si.page_url != '' AND si.page_url IS NOT NULL AND
`

var sqlCorrelatedSitesBody = `
WITH PartnerSources AS (
    SELECT * FROM find_correlated_sources_by_domain($1)
),
WhoisAndSSLInfo AS (
    SELECT
        ps.source_id,
        ps.url,
        ni.created_at,
        ni.details->'whois' AS whois_info,
        hi.details->'ssl_info' AS ssl_info
    FROM
        PartnerSources ps
    JOIN
        SourceSearchIndex ssi ON ps.source_id = ssi.source_id
    LEFT JOIN
        NetInfoIndex nii ON ssi.index_id = nii.index_id
    LEFT JOIN
        NetInfo ni ON nii.netinfo_id = ni.netinfo_id
    LEFT JOIN
        HTTPInfoIndex hii ON ssi.index_id = hii.index_id
    LEFT JOIN
        HTTPInfo hi ON hii.httpinfo_id = hi.httpinfo_id
    WHERE
        (ni.details->'whois' IS NOT NULL OR hi.details->'ssl_info' IS NOT NULL)
)
SELECT DISTINCT
    source_id,
    url,
    created_at,
    whois_info,
    ssl_info
FROM
    WhoisAndSSLInfo
WHERE
`

var sqlNetInfoBody = `
SELECT DISTINCT
    ni.created_at,
    ni.last_updated_at,
    ni.details
FROM
    NetInfo ni
JOIN
    NetInfoIndex nii ON ni.netinfo_id = nii.netinfo_id
JOIN
    SearchIndex si ON nii.index_id = si.index_id
LEFT JOIN
    KeywordIndex ki ON si.index_id = ki.index_id
LEFT JOIN
    Keywords k ON ki.keyword_id = k.keyword_id
WHERE
`

var sqlHTTPInfoBody = `
SELECT DISTINCT
    hi.created_at,
    hi.last_updated_at,
    hi.details
FROM
    HTTPInfo hi
JOIN
    HTTPInfoIndex hii ON hi.httpinfo_id = hii.httpinfo_id
JOIN
    SearchIndex si ON hii.index_id = si.index_id
LEFT JOIN
    KeywordIndex ki ON si.index_id = ki.index_id
LEFT JOIN
    Keywords k ON ki.keyword_id = k.keyword_id
WHERE
`
