// name: math_utils_tests
// description: Unit-Tests for math_utils lib_plugin
// type: test_plugin

/*
 * Unit tests for math_utils lib_plugin
 */

test("add() adds two numbers", function () {
	var r = result.add(2, 3);
	assertEqual(r, 5);
});

test("subtract() subtracts two numbers", function () {
	var r = result.subtract(10, 4);
	assertEqual(r, 6);
});

test("clamp() clamps value inside range", function () {
	var r = result.clamp(5, 0, 10);
	assertEqual(r, 5);
});

test("clamp() clamps value below min", function () {
	var r = result.clamp(-5, 0, 10);
	assertEqual(r, 0);
});

test("clamp() clamps value above max", function () {
	var r = result.clamp(42, 0, 10);
	assertEqual(r, 10);
});

test("add() throws on invalid arguments", function () {
	var failed = false;
	try {
		result.add("a", 3);
	} catch (e) {
		failed = true;
	}
	assertTrue(failed, "expected add() to throw");
});

test("clamp() throws if min > max", function () {
	var failed = false;
	try {
		result.clamp(5, 10, 0);
	} catch (e) {
		failed = true;
	}
	assertTrue(failed, "expected clamp() to throw");
});
