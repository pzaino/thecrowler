# Integration, Performance and Fuzzing tests

To run these tests you'll need to install some tools:

## Performance tests

I use k6 for the API performance tests.

On mac:

```bash
brew install k6
```

On Linux:

- Install go lang first

```bash
go install go.k6.io/k6@latest
```

To run the tests:

- Make sure the API is app and running and reachable via localhost:8080

```bash
cd ./tests/perf
./run_api_all
```

## API Fuzzing tests

I use ffuf to fuzz the API.

on mac:

```bash
brew install ffuf
```

On Linux:

- First make sure you have go installed

- Then

```bash
go install github.com/ffuf/ffuf/v2@latest
```

To run the tests:

- Make sure the API is up and running and reachable to localhost:8080

- Then

```bash
cd ./tests/fuzz
./run_api_all
```
