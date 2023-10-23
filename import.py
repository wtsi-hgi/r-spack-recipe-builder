import hashlib
import requests
import pyreadr
import pandas as pd
import os
import json
from packaging.version import parse
import pickle
import email.utils, datetime

print("Fetching package versions")
stream = os.popen("singularity run /opt/spack.sif list --format version_json")
builtin = stream.read()
stream.close()
builtin = json.loads(builtin)
print("Versions successfully fetched!\n")
packageVersions = {}
locations = {}
for i in builtin:
	packageVersions[i["name"]] = i["versions"]
	locations[i["name"]] = i["file"]
def importPackage(package, version, type):
	if type == "":
		version = "latest"
		type = "latest"
	urlbool = True

	packman = "pypi"
	url = f"https://pypi.org/pypi/{package}/json"
	r = requests.get(url)
	record = r.json()
	
	if record["message"] == "Not Found":
		raise Exception(f"Package {'r-' + package.lower().replace('.','-')} not found in database")

	if version == "latest" or version == "":
		version = record["info"]["version"]
	
	name, description = record["info"]["name"], record["info"]["summary"]

	def writeDeps(field):
		if record["info"][field] == None:
			return []
		elif pd.isna(record["info"][field]):
			return []
		newList = [
			dep.replace(" ", "")
			for dep in record["info"][field]
		]
		return newList

	dependencies = f'Python ({record["info"]["requires_python"]})'
	dependencies += writeDeps("requires_dist")

	try:
		packageURL = record["info"]["home_page"]
	except:
		urlbool = False

	versions = []
	versions.append(f"""	version("{record['Version']}", sha256="{sha256_hash_latest.hexdigest()}")\n""")
	if type != "latest" and type != "any":
		for i in record["releases"][version]:
			if i["python_version"] == "source":
				pkg = i
		versions.append(f"""	version("{version}", sha256="{pkg["digests"]["sha256"]}")\n""")
	if "py-" + package.lower().replace(".","-") in packageVersions.keys():
		if os.path.isdir("packages/py-" + package.lower().replace(".","-")) or type == "any":
			print(f"package {'py-' + package.lower().replace('.','-')} already exists")
			return(f"Package {'py-' + package.lower().replace('.','-')} already exists")
		else:

			if "at least" in type:
				verNum = type.split("at least ")[1]
				if parse(verNum) <= parse(packageVersions["py-" + package.lower().replace(".","-")][0]):
					print(f"package {'py-' + package.lower().replace('.','-')} already exists")
					return(f"Package {'py-' + package.lower().replace('.','-')} already exists")
			elif version == "latest":
				if record["info"]["version"] in packageVersions["py-" + package.lower().replace(".","-")]:
					print(f"package {'py-' + package.lower().replace('.','-')} already exists")
					return(f"Package {'py-' + package.lower().replace('.','-')} already exists")
			else:
				if type in packageVersions["py-" + package.lower().replace(".","-")]:
					print(f"package {'py-' + package.lower().replace('.','-')} already exists")
					return(f"Package {'py-' + package.lower().replace('.','-')} already exists")
			for i in getVersions(package):
					versions.append(str(i).replace("    ", "\t") + "\n")
	os.makedirs("packages/r-" + package.lower().replace(".","-"))

	f = open("packages/r-" + package.lower().replace(".","-") + "/package.py", "w")

	dependencylist = []
	variants = []
	for k in dependencies:
		try:
			dependencylist.append("\tdepends_on(\"" + packageName(k)[0] + "\", type=(\"build\", \"run\"))\n")
		except:
			continue
	
	classname = package.split(".")
	for i in range(len(classname)):
		classname[i] = classname[i].capitalize()
	classname = "".join(classname)

	backslashN = "\n\t"

	f.write(f"""# Copyright 2013-2023 Lawrence Livermore National Security, LLC and other
# Spack Project Developers. See the top-level COPYRIGHT file for details.
#
# SPDX-License-Identifier: (Apache-2.0 OR MIT)
	
from spack.package import *
	
			
class {classname}(PythonPackage):
	\"\"\"{name}

	{description}
	\"\"\"
	
	{f'homepage = "{packageURL}"{backslashN}' if urlbool and str(packageURL) != "nan" else ''}{packman} = "{package}"

{"".join(versions)}

{"".join(dependencylist)}
{"".join(variants)}
""")

	f.close()
	print(f"Package {'r-' + package.lower().replace('.','-')} has been created!")

def packageName(k):
	if "(" in k:
		version = k.split("(")
		k = version[0]
		version = version[1].lower().replace(")","")
		getversion = ""
		if ">=" in version:
			fullname = k.replace(".","-")
			version
			getversion = f"@{version[2:]}:"
			type = "at least " + version[2:]
		elif "<=" in version:
			fullname = k.replace(".","-")
			getversion = f"@:{version[2:]}"
			type = version[2:]
		else:
			fullname = k.replace(".","-")
			getversion = f"@={version[2:]}"
			type = version[2:]
	else:
		fullname = k.replace(".","-")
		getversion = ""
		type = "any"
	if fullname.lower() == "r":
		return "r", getversion, type, False
	try:
		importPackage(k, getversion, type)	
	except:
		raise Exception(f"Package {'r-' + k.lower().replace('.','-')} not found in database")
	# print(fullname.lower(), getversion, type)
	return "r-" + fullname.lower(), getversion, type, False

def getVersions(package):
	versions = []
	if "https://" in locations["r-" + package.lower().replace(".","-")]:
		location = locations["r-" + package.lower().replace(".","-")].replace("https://github.com","https://raw.githubusercontent.com").replace("blob/", "")
		recipe = requests.get(location, allow_redirects=True)
		pyfile = str(recipe.content)[2:].split("\\n")
	else:
		recipe = open(locations["r-" + package.lower().replace(".","-")], "r")
		pyfile = str(recipe.content)[2:].split("\\n")
		pyfile = recipe.readlines()
	for i in pyfile:
			if "version(" in i:
				versions.append(i)
	return(versions)

package = str(input("Please enter package name: "))
version = str(input("Please enter package version (leave blank for latest version): "))

importPackage(package, version, version)