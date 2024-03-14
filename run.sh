#! /bin/sh

cd "$(dirname "$0")"

echo "Installing requirements if necessary..."
pip install -r requirements.txt > /dev/null 2>&1
python3 RPackageCreator.py