import json
import os
import subprocess
import requests
import ast
import sys

spackBin = "spack"

def getExistingVersions():
	os.makedirs("packages", exist_ok=True)
	os.makedirs("libs", exist_ok=True)
	print("Fetching package versions, this could take a while...")
	stream = subprocess.run([spackBin, "list", "--format", "version_json", "py-*"], capture_output=True)
	decoded = stream.stdout.decode("utf-8").strip()
	builtin = json.loads(decoded)
	print("Versions successfully fetched!\n")
	packageVersions = {}
	for row in builtin:
		packageVersions[row["name"]] = row["versions"]
	return packageVersions

def getPyPiJson(package_name):
	request = requests.get(f"https://pypi.org/pypi/{package_name}/json")
	if request.status_code != 200:
		print(f"Failed to retrieve package {package_name}")
		exit()
	return request.json()

def pyify(package):
	if package == "python" or package.startswith("python@"):
		return package
	return "py-" + package.lower().replace(".","-").replace("_","-").replace(" ", "-").split("[")[0]

def spackifyVersion(version: str):
	if ">=" in version:
		result = version.replace(">=", "@") + ":"
	elif "<=" in version:
		result = version.replace("<=", "@:")
	else :
		result = version.replace("=", "@", 1)
		while "=" in result:
			result = result.replace("=", "")
	return result

def getVersions(versionList):
	versions = []
	filename = ""
	extradeps = {}
	for i in versionList.keys():
		if versionList[i] == []:
			continue
		info = []
		possibleWheels = []
		for j in range(len(versionList[i])):
			if versionList[i][j]["packagetype"] == "bdist_wheel" and (versionList[i][j]["filename"].endswith("manylinux1_x86_64.whl") or versionList[i][j]["filename"].endswith("any.whl")):
				possibleWheels.append(versionList[i][j])
		if possibleWheels != []:
			for j in possibleWheels:
				if info == []:
					info = j
				elif float(j["python_version"].replace("cp", "").replace("py", "")) > float(info["python_version"].replace("cp", "").replace("py", "")):
					info = j
		else:
			for j in range(len(versionList[i])):
				if versionList[i][j]["packagetype"] == "sdist":
					info = versionList[i][j]
					break
		if info == []:
			continue
		if info["yanked"] == True:
			continue
		
		if info['packagetype'] == 'bdist_wheel':
			cmd = subprocess.run(["pyPIMD/pypi", info["url"]], capture_output=True)
			decoded = cmd.stdout.decode("utf-8").split("\n\n")[0].split("\n")
			depends = []
			for j in decoded:
				if j.startswith("Requires-Dist:"):
					depends.append(j.replace("Requires-Dist: ", "").replace(" ", ""))
			for j in depends:
				if j in extradeps.keys():
					extradeps[j].append(i)
				else:
					extradeps[j] = [i]
			versions.append(f"\tversion(\"{i}\", sha256=\"{info['digests']['sha256']}\", expand=False, url=\"{info['url']}\")\n")
		else:
			versions.append(f"\tversion(\"{i}\", sha256=\"{info['digests']['sha256']}\")\n")
		filename = info["filename"]
	return versions, filename, extradeps

def getClassname(package):
	classname = package.replace("-",".").replace("_",".").split(".")
	for i in range(len(classname)):
		classname[i] = classname[i].capitalize()
	return "".join(classname)

def writeRecipe(header, footer, versions, depends, package):
	os.makedirs("packages/" + pyify(package), exist_ok=True)
	f = open("packages/" + pyify(package) + "/package.py", "w")
	f.write(f"""{header}

{"".join(versions)}
{"".join(depends)}{footer}""")
	f.close()
	print(f"	✅ Package {package} successfully created!")

def getDepends(dependencies):
	depends_on = []
	for i in dependencies:
		depends_on.append("\tdepends_on(\"" + pyify(i) + "\", type=(\"build\", \"run\"))\n")
	return depends_on

def getTemplate(mode, package, description, homepage, classname, filename):
	if mode == "+":
		header = f"""# Copyright 2013-2023 Lawrence Livermore National Security, LLC and other
# Spack Project Developers. See the top-level COPYRIGHT file for details.
#
# SPDX-License-Identifier: (Apache-2.0 OR MIT)

from spack.package import *


class Py{classname}(PythonPackage):
	\"\"\"{description}\"\"\"
	
	homepage = "{homepage}"
	pypi = "{package}/{filename}" """
		footer = ""
	else:
		with open("packages/" + pyify(package) + "/package.py", "r") as f:
			lines = ast.parse(f.read())
		firstline = 0
		for node in ast.walk(lines):
			if isinstance(node, ast.Assign) or isinstance(node, ast.Call):
				if (isinstance(node.value.func, ast.Name) and node.value.func.id == "version") or (isinstance(node.value.func, ast.Attribute) and (node.value.func.attr == "url" or node.value.func.attr == "urls")):
					firstline = i
					break
		header = "".join(lines[:firstline]).strip()
		lastline = len(lines)
		for node in ast.walk(lines):
			if isinstance(node, ast.Assign) or isinstance(node, ast.Call):
				if (isinstance(node.value.func, ast.Name) and node.value.func.id == "depends_on") or (isinstance(node.value.func, ast.Attribute) and (node.value.func.attr == "depends_on")):
					lastline = i + 1
					break
		for i in range(len(lines)):
			lines[i] = lines[i].replace("    ", "\t")
			if "\tversion(" in lines[i] or "\turl =" in lines[i] or "\turls =" in lines[i]:
				firstline = i
				break
		header = "".join(lines[:firstline]).strip()
		lastline = len(lines)
		for j in range(len(lines)):
			lines[j] = lines[j].replace("    ", "\t")
			if "\tdepends_on(" in lines[j] or "\tversion(" in lines[j]:
				ast.parse()
				lastline = j + 1
				break
		footer = "".join(lines[lastline:])
	return header, footer

def get(package_name, package_version, recurse=False):
	if pyify(package_name) in existingVersions:
		print(f"	✴️ {pyify(package_name)} already exists in spack") 
		return
	url = f"https://libraries.io/api/pypi/{package_name}/{package_version}/dependencies?api_key=ebb39aed4c41baa4c4e8a384a8775cd9"
	response = requests.get(url)
	if response.status_code != 200:
		print(f"	❌ Failed to retrieve package {package_name}")
		return
	
	json = response.json()
	dependencies = []
	pypiRequest = getPyPiJson(package_name)
	python_version = spackifyVersion(pypiRequest["info"]["requires_python"])
	if python_version != "":
		dependencies.append("python"+python_version)
	for i in json["dependencies"]:
		if str(i["platform"]).lower() == "pypi" and i["optional"] == False:
			dependencies.append(str(i["project_name"]).lower())
			if recurse:
				get(str(i["project_name"]).lower(), i["latest_stable"], True)
		else:
			continue
	
	versions, filename, extradeps = getVersions(pypiRequest["releases"])
	header, footer = getTemplate("+", package_name, json["description"], json["homepage"], getClassname(package_name), filename)
	dependencies = getDepends(dependencies)
	if extradeps != {}:
		footer += f"\n# {str(extradeps)}"
	writeRecipe(header, footer, versions, dependencies, package_name)

package_version = "latest"
existingVersions = getExistingVersions()
if len(sys.argv) < 2:
	print("Usage: python3 PyPackageCreator.py package_name [...package_name]")
	exit()
for i in range(1, len(sys.argv)):
	print(f"Building recipes for {sys.argv[i]}...")
	get(sys.argv[i], package_version, True)
