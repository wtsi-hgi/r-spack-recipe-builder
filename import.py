import hashlib
import requests
import pyreadr
import tempfile
import pandas as pd
import os
import json
import packaging


#Makes CRAN dataframe
#TODO: if newer thing exists download it
databaseurl = "https://cran.r-project.org/web/packages/packages.rds"
response = requests.get(databaseurl, allow_redirects=True)
tmpfile = tempfile.NamedTemporaryFile()
tmpfile.write(response.content)

database = pyreadr.read_r(tmpfile.name)
database = database[None]
pandasDatabase = pd.DataFrame(database)

#Makes Bioconductor dictionary
biocURL = "https://www.bioconductor.org/packages/3.18/bioc/VIEWS"
response = requests.get(biocURL, allow_redirects=True)
databaseBIOC = (response.text).split("\n\n")
packagesBIOC = {}
for i in range(len(databaseBIOC) - 1):
	databaseBIOC[i] = databaseBIOC[i].replace("\n        ", " ")
	databaseBIOC[i] = databaseBIOC[i].split("\n")
	newPackage = {}
	for j in range(len(databaseBIOC[i])):
		databaseBIOC[i][j] = databaseBIOC[i][j].split(": ", 1)
		newPackage[databaseBIOC[i][j][0]] = databaseBIOC[i][j][1]
	packagesBIOC[newPackage["Package"]] = newPackage


stream = os.popen("singularity run /opt/spack.sif list --format version_json r-*")
builtin = stream.read()
stream.close()
builtin = json.loads(builtin)
packageVersions = {}
locations = {}
for i in builtin:
	packageVersions[i["name"]] = i["versions"]
	locations[i["name"]] = i["file"]

def importPackage(package, version, type):
	if version == "":
		version = "latest"
		type = "latest"

	urlbool = True

	record = pandasDatabase.loc[pandasDatabase['Package'] == package]
	if len(record) == 0:
		if packagesBIOC.get(package) == None:
			raise Exception(f"Package {'r-' + package.lower().replace('.','-')} not found in database")
		else:
			record = packagesBIOC[package]
			packman = "bioc"
	else:
		record = record.to_dict('records')[0]
		packman = "cran"

	name, description = record["Title"], record["Description"]

	def writeDeps(field):
		if packagesBIOC.get(field) == None:
			return []
		elif pd.isna(record[field]):
			return []
		return record[field].replace(" ","").replace("\n", "").split(",")

	dependencies = writeDeps("Depends")
	dependencies += writeDeps("Imports")
	dependencies += writeDeps("LinkingTo")
	suggests = writeDeps("Suggests")

	try:
		packageURL = record["URL"].split(",")[0].split(" ")[0].split("\n")[0]
	except:
		urlbool = False
	
	if packman == "cran":
		baseurl_version = f"https://cran.r-project.org/src/contrib/Archive/{package}/{package}_{version}.tar.gz"
		baseurl_latest = f"https://cran.r-project.org/src/contrib/{package}_{record['Version']}.tar.gz"
	if packman == "bioc":
		baseurl_latest = f"https://bioconductor.org/packages/release/bioc/src/contrib/{package}_{version}.tar.gz"
	elif type != "latest":
		source = requests.get(baseurl_version, allow_redirects=True)
		sha256_hash_version = hashlib.sha256()
		sha256_hash_version.update(source.content)
	latest = requests.get(baseurl_latest, allow_redirects=True)
	sha256_hash_latest = hashlib.sha256()
	sha256_hash_latest.update(latest.content)

	versions = []
	versions.append(f"""	version("{record['Version']}", sha256="{sha256_hash_latest.hexdigest()}")\n""")
	if "r-" + package.lower().replace(".","-") in packageVersions.keys():
		if os.path.isdir("packages/r-" + package.lower().replace(".","-")):
			return(f"Package {'r-' + package.lower().replace('.','-')} already exists")
		if type == "any":
			return(f"Package {'r-' + package.lower().replace('.','-')} already exists")
		else:

			if "at least" in type:
				verNum = type.split("at least ")[1]
				if packaging.version.parse(verNum) <= packaging.version.parse(packageVersions["r-" + package.lower().replace(".","-")][0]):
					print(f"Package {'r-' + package.lower().replace('.','-')} already exists")
					return(f"Package {'r-' + package.lower().replace('.','-')} already exists")
			elif version == "latest":
				if record["Version"] in packageVersions["r-" + package.lower().replace(".","-")]:
					print(f"Package {'r-' + package.lower().replace('.','-')} already exists")
					return(f"Package {'r-' + package.lower().replace('.','-')} already exists")
			else:
				if type in packageVersions["r-" + package.lower().replace(".","-")]:
					print(f"Package {'r-' + package.lower().replace('.','-')} already exists")
					return(f"Package {'r-' + package.lower().replace('.','-')} already exists")
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
	for l in suggests:
		try:
			pkg = packageName(l)
			dependencylist.append("\tdepends_on(\"" + pkg[0] + pkg[1] + "\", when=\"+" + pkg[0] + "\", type=(\"build\", \"run\"))\n")
			variants.append("\tvariant(\"" + pkg[0] + "\", default=" + str(pkg[3]) + ", description=\"Enable " + pkg[0] + " support\")\n")
		except:
			continue

	classname = package.split(".")
	for i in range(len(classname)):
		classname[i] = classname[i].capitalize()
	classname = "".join(classname)

	f.write(f"""# Copyright 2013-2023 Lawrence Livermore National Security, LLC and other
# Spack Project Developers. See the top-level COPYRIGHT file for details.
#
# SPDX-License-Identifier: (Apache-2.0 OR MIT)
	
from spack.package import *
	
			
class R{classname}(RPackage):
	\"\"\"{name}

	{description}
	\"\"\"
	
	{f'homepage = "{packageURL}"' if urlbool and str(packageURL) != "nan" else ''}
	{packman} = "{package}"

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