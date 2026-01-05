// name: api_ping_pong
// description: Simple ping-pong API test plugin (example of API plugin)
// version: 1.0.0
// type: api_plugin
// api_endpoint: /v1/plugin/ping
// api_methods: GET, POST
// api_auth: none
// api_auth_type: none

/*
 * Simple ping-pong API plugin.
 * It returns "pong" and echoes back basic request info.
 */

function executePlugin(event, jsonData) {
    var response = {
        pong: true,
        timestamp: new Date().toISOString()
    };

    // Echo input if present
    if (jsonData && jsonData.input) {
        response.input = jsonData.input;
    }

    // Echo HTTP context if present
    if (jsonData && jsonData.http) {
        response.http = {
            method: jsonData.http.method,
            path: jsonData.http.path,
            query: jsonData.http.query
        };
    }

    return response;
}

// Required CROWler calling convention
result = executePlugin(params.event, params.jsonData);
result;
