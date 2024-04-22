import os
import subprocess
from bs4 import BeautifulSoup as bs
import requests
import sys

spackBin = "spack"

def getRepos():
	os.makedirs("packages", exist_ok=True)
	cmd = subprocess.run([spackBin, "repo", "list"], capture_output=True)
	repoDirs = cmd.stdout.decode("utf-8").strip()
	splittedDirs = repoDirs.split("\n")
	actualDirs = []
	for dir in splittedDirs:
		actualDirs.append(dir.split(" ", 1)[1].strip())
	return actualDirs

def rify(package):
	return "r-" + package.lower().replace(".","-").replace("_", "-").replace("++", "pp").replace("+", "-plus")

def getVersions(package, packman):
	if packman == "cran":
		req = requests.get(f"https://cran.r-project.org/web/packages/{package}/index.html")
		source = "https://cran.r-project.org/src/contrib/"
	elif packman == "bioc":
		req = requests.get(f"https://bioconductor.org/packages/{package}/")
		source = "https://bioconductor.org/packages/release/bioc/src/contrib/"
	if req.status_code != 200:
		print("Package not found")
		exit()
	s = bs(req.text, "html.parser")
	for link in s.find_all("a"):
		if "src/contrib/" in link.get("href"):
			source += link.get("href").split("/")[-1]
			break

	cmd = subprocess.run([spackBin, "create", "-fb", "--skip-editor", source], capture_output=True)
	location = cmd.stdout.decode("utf-8").strip().split("\n")[-1].replace("==> Created package file: ", "")
	with open(location) as file:
		lines = file.readlines()
	
	i = 0
	versions = []
	for i in range(len(lines)):
		if "version(\"" in lines[i]:
			versions.append("\t" + lines[i].strip() + "\n")
	
	return versions

def get(package, repos):
	location = []
	for repo in repos:
		if os.path.isdir(repo + "/packages/" + rify(package)):
			location = repo
			break

	if location == []:
		print("Package not found")
		exit()
	
	with open(location + "/packages/" + rify(package) + "/package.py") as file:
		lines = file.readlines()

	i = 0
	packman = ""
	versionLine = 0
	while i < len(lines):
		if "cran = " in lines[i]:
			packman = "cran"
			i += 1
		elif "bioc = " in lines[i]:
			packman = "bioc"
			i += 1
		elif "version(\"" in lines[i]:
			lines.pop(i)
			versionLine = i
		else:
			i += 1
	
	versions = getVersions(package, packman)
	for version in versions:
		lines.insert(versionLine, version)
	with open("packages/" + rify(package) + "/package.py", "w") as file:
		file.write("".join(lines))
	print(f"Versions added to {os.getcwd()}/packages/{rify(package)}/package.py")
			
if len(sys.argv) < 2:
	print("Usage: python3 RVersionExpander.py package_name [...package_name]")
	exit()
repos = getRepos()
for i in range(1, len(sys.argv)):
	get(sys.argv[i], repos)