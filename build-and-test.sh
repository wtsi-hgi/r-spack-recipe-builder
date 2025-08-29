#!/bin/zsh

set -e

docker build -t spack-with-uv:latest -f /home/ubuntu/r-spack-recipe-builder/spack-img/spack.docker /home/ubuntu/r-spack-recipe-builder/spack-img
singularity build --force /home/ubuntu/spack.sif docker-daemon://spack-with-uv:latest

singularity run --bind /usr/bin/zsh --bind /mnt/data /home/ubuntu/spack.sif install py-peppy