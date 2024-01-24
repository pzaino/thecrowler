
# Contributing to TheCROWler

We love your input! We want to make contributing to this project as easy and
transparent as possible, whether it's:

- Reporting a bug
- Discussing the current state of the code
- Submitting a fix
- Proposing new features
- Becoming a maintainer

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

On the Mac:

```bash
brew install pre-commit
```

Once you have pre-commit installed, fork TheCROWler and clone it to your
development machine. Then, in the root directory of the repository of the
project, run the following command to install the pre-commit hooks:

```bash
pre-commit install
```

This will install the pre-commit hooks and will run them on every commit you
make. If any of the hooks fail, the commit will fail and you'll have to fix
the issues before you can commit.

We actively welcome your pull requests:

1. Fork the repo and create your branch from our `develop` branch.
2. If you've added code that should be tested, add tests.
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

- 4 spaces for indentation rather than tabs
- You can try running `gofmt` for style unification

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
