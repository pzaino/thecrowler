// name: json_query_tests
// description: Unit tests for the json_query lib_plugin
// type: test_plugin

var fixture = {
    details: {
        title: "Hello",
        contacts: [
            { email: "a@test.com", age: 20, active: true, tags: ["staff", "blue"] },
            { email: "b@test.com", age: 30, active: false, tags: ["guest"] }
        ],
        tags: ["a", "b", "c", "d"],
        nested: {
            one: { target: 1 },
            two: [{ target: 2 }, { other: 3 }]
        },
        "a.b": { "x[y]": 42 },
        "": "empty-key"
    }
};

test("queries a simple child path", function () {
    assertDeepEqual(result.all(fixture, "details.title"), ["Hello"]);
});

test("accepts an explicit root selector", function () {
    assertDeepEqual(result.all(fixture, "$.details.contacts[*].email"), ["a@test.com", "b@test.com"]);
});

test("queries a root array", function () {
    assertDeepEqual(result.all([{ id: 1 }, { id: 2 }], "[*].id"), [1, 2]);
});

test("returns every valid JSON root type", function () {
    assertDeepEqual(result.all({ a: 1 }, "$"), [{ a: 1 }]);
    assertDeepEqual(result.all([1, 2], "$"), [[1, 2]]);
    assertDeepEqual(result.all("hello", "$"), ["hello"]);
    assertDeepEqual(result.all(12.5, "$"), [12.5]);
    assertDeepEqual(result.all(true, "$"), [true]);
    assertDeepEqual(result.all(null, "$"), [null]);
});

test("parses JSON text with primitive and compound roots", function () {
    assertDeepEqual(result.queryJSON("null", "$"), [null]);
    assertDeepEqual(result.queryJSON("\"hello\"", "$"), ["hello"]);
    assertDeepEqual(result.queryJSON("[{\"id\":1}]", "[*].id"), [1]);
});

test("supports negative array indexes", function () {
    assertDeepEqual(result.all(fixture, "details.tags[-1]"), ["d"]);
});

test("supports forward and reverse slices", function () {
    assertDeepEqual(result.all(fixture, "details.tags[0:4:2]"), ["a", "c"]);
    assertDeepEqual(result.all(fixture, "details.tags[::-1]"), ["d", "c", "b", "a"]);
});

test("supports union selectors", function () {
    assertDeepEqual(result.all(fixture, "details.contacts[1,0].email"), ["b@test.com", "a@test.com"]);
    assertDeepEqual(result.all({ a: 1, b: 2 }, "['b','a']"), [2, 1]);
});

test("supports quoted and empty property names", function () {
    assertDeepEqual(result.all(fixture, "details['a.b']['x[y]']"), [42]);
    assertDeepEqual(result.all(fixture, "details['']"), ["empty-key"]);
});

test("supports recursive descent", function () {
    assertDeepEqual(result.all(fixture, "details.nested..target"), [1, 2]);
});

test("supports deterministic object wildcards", function () {
    assertDeepEqual(result.all({ z: 1, a: 2 }, "*"), [2, 1]);
});

test("returns canonical paths", function () {
    assertDeepEqual(result.paths(fixture, "details.contacts[*].email"), [
        "$['details']['contacts'][0]['email']",
        "$['details']['contacts'][1]['email']"
    ]);
});

test("first, exists and count have explicit missing-value semantics", function () {
    assertEqual(result.first(fixture, "details.missing", "fallback"), "fallback");
    assertTrue(result.exists(fixture, "details.title"));
    assertFalse(result.exists(fixture, "details.missing"));
    assertEqual(result.count(fixture, "details.contacts[*]"), 2);
});

test("filters numeric and boolean values", function () {
    assertDeepEqual(result.all(fixture, "details.contacts[?(@.age >= 25)].email"), ["b@test.com"]);
    assertDeepEqual(result.all(fixture, "details.contacts[?(@.active == true)].email"), ["a@test.com"]);
});

test("filters with regular expressions", function () {
    assertDeepEqual(result.all(fixture, "details.contacts[?(@.email =~ /TEST/i)].email"), ["a@test.com", "b@test.com"]);
});

test("filters can reference the document root", function () {
    var document = { limit: 25, rows: [{ n: 20 }, { n: 30 }] };
    assertDeepEqual(result.all(document, "rows[?(@.n > $.limit)].n"), [30]);
});

test("filters provide safe helper functions", function () {
    assertDeepEqual(result.all(fixture, "details.contacts[?(startsWith(@.email, 'a@'))].email"), ["a@test.com"]);
    assertDeepEqual(result.all(fixture, "details.contacts[?(contains(@.tags, 'staff'))].email"), ["a@test.com"]);
    assertDeepEqual(result.all(fixture, "details.contacts[?(length(@.tags) == 2)].email"), ["a@test.com"]);
    assertDeepEqual(result.all(fixture, "details.contacts[?(type(@.age) == 'number')].email"), ["a@test.com", "b@test.com"]);
});

test("projects records relative to each selected base node", function () {
    assertDeepEqual(result.project(fixture, "details.contacts[*]", {
        email: "email",
        age: "age",
        missing: { path: "missing", "default": "n/a" },
        tag_count: { path: "tags[*]", mode: "count" },
        has_email: { path: "email", mode: "exists" }
    }), [
        { email: "a@test.com", age: 20, missing: "n/a", tag_count: 2, has_email: true },
        { email: "b@test.com", age: 30, missing: "n/a", tag_count: 1, has_email: true }
    ]);
});

test("projects absolute paths from the original root", function () {
    assertDeepEqual(result.project(fixture, "details.contacts[0]", {
        local_email: "email",
        document_title: "$.details.title"
    }), [{ local_email: "a@test.com", document_title: "Hello" }]);
});

test("validates JSON-compatible native values", function () {
    assertTrue(result.validate({ a: [1, null, true, "x"] }));
});

test("rejects non-finite JSON numbers", function () {
    assertThrows(function () { result.validate({ value: Infinity }); });
});

test("rejects cycles during validation and recursive descent", function () {
    var cyclic = {};
    cyclic.self = cyclic;
    assertThrows(function () { result.validate(cyclic); });
    assertThrows(function () { result.all(cyclic, "..*"); });
});

test("rejects malformed paths and zero slice steps", function () {
    assertThrows(function () { result.compile("a["); });
    assertThrows(function () { result.compile("a[::0]"); });
});

test("enforces result limits", function () {
    assertThrows(function () {
        result.all(fixture, "details.contacts[*]", { maxResults: 1 });
    });
});

test("compiled paths are reusable", function () {
    var compiled = result.compile("details.contacts[*].email");
    assertDeepEqual(result.all(fixture, compiled), ["a@test.com", "b@test.com"]);
});

test("path cache is bounded", function () {
    result.clearCache();
    result.setCacheSize(2);
    result.compile("details.title");
    result.compile("details.tags[*]");
    result.compile("details.contacts[*]");
    assertDeepEqual(result.cacheStats(), { size: 2, capacity: 2 });
    result.setCacheSize(256);
});
