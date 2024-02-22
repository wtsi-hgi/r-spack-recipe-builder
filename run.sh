#! /bin/sh

cd "$(dirname "$0")"

python3 RPackageCreator.py
echo "All recipes have been created. Terminal closing in 10 seconds..."
sleep 10