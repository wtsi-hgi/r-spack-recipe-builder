# Repository Guidelines

This guide for iteratively improving the script `cmd/py-package-uv-creator/main.go` and its test main_test.go. **Important: Under no circumstances should you ask for confirmation or preference for further actions from the user for any action, there is no user interaction, because you are running inside an automated pipeline. Always make the changes that you suggest and proceed.**

## Workflow
1) when asked to create a uv package for a pypi entry, first run the tool `py-package-uv-creator -f {pypi_name}` to create a uv package.
2) then run `spack install` on the newly created uv package.
3) If there is any errors, first look at the created recipe in `packages/{pkg}/package.py` and check its homepage/url just to make sure that the `install_test` session is correct. Some python packages may have a different import name than the package name, so you should modify the `import_modules` field and the test code.
4) Otherwise you need to adjust the `cmd/py-package-uv-creator/main.go` to find out the correct way to create the uv package recipe. 
5) If you make any changes to the `cmd/py-package-uv-creator/main.go` file, you need to also add appropriate tests to the `cmd/py-package-uv-creator/main_test.go` file.
6) Then you need to make sure the tests pass by running `go test ./cmd/py-package-uv-creator/...`
7) If the tests pass, run `make build` to build the tool and test it with `./py-package-uv-creator -f {pypi_name}` again, back to step 2.
8) If the installation succeeds, and no changes are needed, you can exit.
9) If any changes are made, make a commit with an appropriate message.


