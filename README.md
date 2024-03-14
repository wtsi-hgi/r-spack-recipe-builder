# Spack Dependency Builder

A python script which creates recipes for all R packages within CRAN and Bioconductor.

## Prerequisites
- You must have spack installed (running `spack` should give a help message)
- You must add the repo folder to *~/.spack/repos.yaml* (See here: https://spack.readthedocs.io/en/latest/repositories.html)

## Usage
Clone the repo, make sure the prerequisites are met, then run the shell script:

    ./run.sh
If you already have recipes in the packages folder, it'll take a few minutes to fetch package versions, if not then it should take a couple seconds.

When you run the script, it will tell you what the different symbols it prints means, I've included it below:

    [+] means a package is freshly created
    [*] means a package is updated
    [~] means a package is already up to date
    [x] means a package can't be created or is blacklisted
I'd recommend having a packages folder separate from this repo that's git initialised, so if you have made individual changes but want to update everything to latest, you can copy- paste from one to the other and review the diffs so you don't lose your work. 

**Working on the packages folder in the repo is a *very bad idea*, the script will happily replace versions and dependencies in any recipe that needs updating or has a system requirement.** Don't say I didn't warn you.
