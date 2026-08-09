## Contributing

[fork]: https://github.com/ishinova/gh-user-attachments/fork
[pr]: https://github.com/ishinova/gh-user-attachments/compare

Hi there! We're thrilled that you'd like to contribute to this project. Your help is essential for keeping it great.

Contributions to this project are [released](https://help.github.com/articles/github-terms-of-service/#6-contributions-under-repository-license) to the public under the [project's open source license](LICENSE).

Please note that this project is released with a [Contributor Code of Conduct](CODE_OF_CONDUCT.md). By participating in this project you agree to abide by its terms.

## Prerequisites

- [mise](https://mise.jdx.dev/getting-started.html)
- [GitHub CLI](https://cli.github.com/) (`gh`) v2.0+

## Local setup

```sh
# Clone the repository (or your fork)
git clone https://github.com/ishinova/gh-user-attachments.git
cd gh-user-attachments

# Install pinned toolchain and dependencies
mise install
mise exec -- go mod download
```

## Build

```sh
mise exec -- go build ./...
```

This produces a `gh-user-attachments` binary in the current directory.

## Test

```sh
# Run all tests with race detection
mise exec -- go test -race -count=1 ./...
```

## Vet

```sh
mise exec -- go vet ./...
```

## Install locally as a `gh` extension

To test your local build as a `gh` CLI extension:

```sh
# Build the binary
mise exec -- go build -o gh-user-attachments .

# Remove any existing installation
gh extension remove user-attachments

# Install from the local directory
gh extension install .
```

You can now run `gh user-attachments` and it will use your locally built version.

## Submitting a pull request

1. [Fork][fork] and clone the repository
1. Create a new branch: `git checkout -b my-branch-name`
1. Make your change and add tests
1. Make sure all local checks pass: `mise run check`
1. Push to your fork and [submit a pull request][pr]

Here are a few things you can do that will increase the likelihood of your pull request being accepted:

- Write tests.
- Keep your change as focused as possible. If there are multiple changes you would like to make that are not dependent upon each other, consider submitting them as separate pull requests.
- Write a [good commit message](http://tbaggery.com/2008/04/19/a-note-about-git-commit-messages.html).

## Resources

- [How to Contribute to Open Source](https://opensource.guide/how-to-contribute/)
- [Using Pull Requests](https://help.github.com/articles/about-pull-requests/)
- [GitHub Help](https://help.github.com)
