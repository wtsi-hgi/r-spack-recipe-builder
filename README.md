# Spack Dependency Builder

A set of python scripts which creates recipes for an R package and its dependencies for Spack.
import.py creates the spack recipe for a specified cran or bioconductor package and its dependencies (it will be able to do python and perl soon)
RPackageCreator.py creates spack recipes for all cran and bioconductor packages

Prerequisites:
    You must be in the bash shell of a singularity image with spack installed. This is done by doing singularity shell {wherever your .sif is} then typing bash and hitting return