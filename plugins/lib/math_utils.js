// name: math_utils
// description: Small math utility library
// type: lib_plugin

/*
 * This is a library plugin.
 * It does NOT execute anything by itself.
 * It only exposes functions to other plugins.
 */

// Ensure we do not pollute global scope accidentally
var math_utils = (function () {

	function isNumber(n) {
		return typeof n === "number" && !isNaN(n);
	}

	function add(a, b) {
		if (!isNumber(a) || !isNumber(b)) {
			throw new Error("add(): arguments must be numbers");
		}
		return a + b;
	}

	function subtract(a, b) {
		if (!isNumber(a) || !isNumber(b)) {
			throw new Error("subtract(): arguments must be numbers");
		}
		return a - b;
	}

	function clamp(value, min, max) {
		if (!isNumber(value) || !isNumber(min) || !isNumber(max)) {
			throw new Error("clamp(): arguments must be numbers");
		}
		if (min > max) {
			throw new Error("clamp(): min must be <= max");
		}
		if (value < min) return min;
		if (value > max) return max;
		return value;
	}

	// Public API
	return {
		add: add,
		subtract: subtract,
		clamp: clamp
	};

})();

// Export library for other plugins
result = math_utils;
