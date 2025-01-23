
# Contributing to TheCROWler

We love your input! We want to make contributing to this project as easy and
transparent as possible, whether it's:

- Reporting a bug
- Discussing the current state of the code
- Submitting a fix
- Proposing new features
- Becoming a maintainer

To check daily updates, you can follow the project on github or click on
[here](https://githubtracker.com/pzaino/thecrowler).

## We Develop with Github

We use GitHub to host code, to track issues and feature requests, as well as
accept pull requests.

## We Use Github Flow, So All Code Changes Happen Through Pull Requests

Pull requests are the best way to propose changes to the codebase (we use
[Github Flow](https://guides.github.com/introduction/flow/index.html)).

Before you start, make sure you have installed pre-commit on your development
machine. To install pre-commit, run the following command:

On most OSes:

```bash
pip install pre-commit
```

On some Linux distributions (like for example OpenSUSE), you may need to
install pre-commit using the following command:

```bash
sudo zypper install python311-pre-commit
```

To have pre-commit available system wide.

**Please Note:** zypper is the package manager for OpenSUSE. You may need to
use a different package manager depending on your Linux distribution.

On the **Mac**, you can install pre-commit using Homebrew:

```bash
brew install pre-commit
```

Another tool used locally is `golangci-lint`. To install it, run the following
command:

```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

And govulncheck:

```bash
go install github.com/ossf/govulncheck/cmd/govulncheck@latest
```

Once you have pre-commit (and the other dependencies) installed, fork TheCROWler
and clone it to your development machine. Then, in the root directory of the
repository of the project, run the following command to install the pre-commit
hooks:

```bash
pre-commit install
```

This will install the pre-commit hooks and will run them on every commit you
make. If any of the hooks fail, the commit will fail and you'll have to fix
the issues before you can commit.

Last bit I recommend is to test you have all pre-commit hooks dependencies
installed. To check this, run the following command:

```bash
pre-commit run --all-files
```

If you have all the dependencies installed, everything should pass. If not,
you'll see a list of errors and warnings. You can install the dependencies
by following the instructions in the error messages.

## How the branches work in TheCROWler

TheCROWler has two main branches:

- `main`: This branch is the one that is used for the releases. It is the
branch that contains the latest stable version of the code. Here stable
means that the code has passed all the local tests and the CI/CD pipeline,
and it's open for the community to run their own tests.
- `develop`: This branch is the one that contains the latest changes that
are being developed. It is the branch that is used to test new features and
fixes before they are merged into the `main` branch. This branch is not
stable and should not be used for production!

All your changes should be based on the `develop` branch and then merged
back into the `develop` branch when they are ready.

In other words, the `main` branch is for users to test the code and the
`develop` branch is for developers to test the integration of their code
with the rest of the codebase.

Every time we have a window of time where the code in `develop` branch
passes all the tests, we merge it into the main branch opening a new test
window for the community. This is the way we ensure that the code in the
 main branch is always stable. Basically the code has to pass:

- All the local tests
- The CI/CD pipeline
- Be reviewed by at least one other developer to be merge in develop
- Pass all CI/CD pipeline tests again to be merged in main

If we have a window of time (usually 2 to 4 weeks) and we are available to
handle a new release, we tag a new release from the main branch.

So, the CROWler supports both basically, CI/CD and a more traditional release
cycle. So users with complex environments can stick to a specific release
until they are ready to move to the next one. While users with more DevOps
orientated environments can use the main branch to test the latest
stable changes which are continuously integrated and deployed.

When you work on a new feature you should create a new branch from the
`develop` branch. When you are ready to merge your changes back into the
`develop` branch, you should create a pull request from your branch to the
`develop` branch.

## Here is the process for submitting a pull request

We actively welcome your pull requests:

1. Fork the repo and create your branch from our `develop` branch.
2. If you've added code that should be tested, add tests. For more information
on testing, see [Test Policy for TheCROWler](doc/test_policy.md).
3. If you've changed APIs, update the documentation.
4. Ensure the test suite passes.
5. Make sure your code lints.
6. Issue that pull request!

## Any contributions you make will be under the Apache 2.0 Software License

In short, when you submit code changes, your submissions are understood to be
under the same [Apache 2.0 License](http://www.apache.org/licenses/LICENSE-2.0)
that covers the project. Feel free to contact the maintainers if that's a
concern.

## Report bugs using Github's issues

We use GitHub issues to track public bugs. Report a bug by
[opening a new issue](https://github.com/yourusername/TheCROWler/issues);
it's that easy!

## Write bug reports with detail, background, and sample code

**Great Bug Reports** tend to have:

- A quick summary and/or background
- Steps to reproduce
  - Be specific!
  - Give sample code if you can.
- What you expected would happen
- What actually happens
- Notes (possibly including why you think this might be happening, or stuff
you tried that didn't work)

People *love* thorough bug reports.

## Use a Consistent Coding Style

- Format rules we follow are the same as the ones used by the Go community
- Format rules are enforced by pre-commit gofmt hook
- For what concern function definitions, if your function can fail, it should
return an error as the last return value. For more information, see
[Error Handling and Go](https://blog.golang.org/error-handling-and-go)
- Ensure you have added or updated unit tests to cover your changes. For more
information, see [Test Policy for TheCROWler](doc/test_policy.md).
- Ensure you comment your code, packages and functions according to the rules
described in [Effective Go](https://golang.org/doc/effective_go.html#commentary)

## Code of Conduct

In the interest of fostering an open and welcoming environment, we as
contributors and maintainers pledge to making participation in our project and
 our community a harassment-free experience for everyone.

## License

By contributing, you agree that your contributions will be licensed under its
 Apache 2.0 License.

## References

This document was adapted from the open-source contribution guidelines for
[Facebook's Draft](https://github.com/facebook/draft-js/blob/master/CONTRIBUTING.md).
