#!/bin/bash

curl -X POST http://localhost:3000/v1/rb \
-H "Content-Type: application/json" \
-d '{"action":"type", "value":"Hello, world!"}'
