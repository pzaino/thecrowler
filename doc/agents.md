# Using Agents with the CROWler

The CROWler allows you to run multiple agents (either in series or in parallel).
 This is useful when you have a large number of agents that you want to run at
 the same time.
 The CROWler will automatically distribute the agents across multiple cores on
 your machine, allowing you to run many agents at once.

Agents are also useful when a user does not wish to code complex plugins or
wish to leverage AI models to perform tasks such ass data validation,
enrichment, correction, manipulation etc. and combine it with actions.

Agents should be defined in YAML files and stored in the `./agents/` path.

Below an example of such YAML file.

## Examples of configuring Agents

```yaml

# Examples of a set of agents configuration file in YAML format:

jobs:
  - name: "Serial Agent 1"
    process: "serial"
    trigger_type: event
    trigger_name: "event_name"
    steps:
      - action: "APIRequest"
        params:
          config:
            url: "http://example.com/api/data"
      - action: "AIInteraction"
        params:
          prompt: "Summarize the following data: $response"
          config:
            url: "https://api.openai.com/v1/completions"
            api_key: "your_api_key"

  - name: "Parallel Agent 1"
    process: "parallel"
    trigger_type: event
    trigger_name: "event_name"
    steps:
      - action: "DBQuery"
        params:
          type: "insert"
          query: "INSERT INTO logs (message) VALUES ('Parallel job 1')"
      - action: "RunCommand"
        params:
          command: "echo 'Parallel job 2'"

  - name: "Serial Agent 2"
    process: "serial"
    trigger_type: agent
    trigger_name: "agent_name"
    steps:
      - action: "PluginExecution"
        params:
          plugin: ["example_plugin"]

```
