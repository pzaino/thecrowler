// Define module if it's not defined
if (typeof module != 'undefined') {
  let module = {};
  // Define module exports
  module.exports = {extends: ['@commitlint/config-conventional']};
}
