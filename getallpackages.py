import hashlib
import time
import requests
import pyreadr
import pandas as pd
import os
import json
import pickle
import email.utils, datetime

#Makes CRAN dataframe
databaseurl = "https://cran.r-project.org/web/packages/packages.rds"
cranHead = requests.head(databaseurl)
cranWebTime = email.utils.parsedate_to_datetime(cranHead.headers.get('last-modified')).replace(tzinfo=None)
cranLocalTime = datetime.datetime.fromtimestamp(os.path.getmtime("cranLibrary.rds"))
if not os.path.isfile("cranLibrary.rds") or cranWebTime > cranLocalTime:
	print("Downloading CRAN database")
	response = requests.get(databaseurl, allow_redirects=True)
	savedDatabase = open("cranLibrary.rds", "wb")
	savedDatabase.write(response.content)
	savedDatabase.close()
savedDatabase = open("cranLibrary.rds", "rb")
database = pyreadr.read_r(savedDatabase.name)
database = database[None]
pandasDatabase = pd.DataFrame(database)

#Makes Bioconductor dictionary
biocURL = "https://www.bioconductor.org/packages/3.18/bioc/VIEWS"
biocHead = requests.head(databaseurl)
biocWebTime = email.utils.parsedate_to_datetime(biocHead.headers.get('last-modified')).replace(tzinfo=None)
biocLocalTime = datetime.datetime.fromtimestamp(os.path.getmtime("biocLibrary.pkl"))
if not os.path.isfile("biocLibrary.pkl"):
	print("Downloading Bioconductor database")
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
	
	with open("biocLibrary.pkl", "wb") as fp:
		pickle.dump(packagesBIOC, fp)

with open('biocLibrary.pkl', 'rb') as fp:
	packagesBIOC = pickle.load(fp)

print("Fetching package versions")
stream = os.popen("singularity run /opt/spack.sif list --format version_json r-*")
builtin = stream.read()
stream.close()
builtin = json.loads(builtin)
print("Versions successfully fetched!\n")
packageVersions = {}
locations = {}
for i in builtin:
	packageVersions[i["name"]] = i["versions"]
	locations[i["name"]] = i["file"]

progress = 0
total = 0

def writePackage(package):
	global progress, total
	progress += 1

	record = pandasDatabase.loc[pandasDatabase['Package'] == package]
	urlbool = True

	if len(record) == 0:
		if packagesBIOC.get(package) == None:
			raise Exception(f"Package {'r-' + package.lower().replace('.','-')} not found in database")
		else:
			record = packagesBIOC[package]
			packman = "bioc"
	else:
		record = record.to_dict('records')[0]
		packman = "cran"

	name, description = record["Title"], record["Description"].replace("\\", "")

	def writeDeps(field):
		if record.get(field) == None:
			return []
		elif pd.isna(record[field]):
			return []
		return record[field].replace(" ","").replace("\n", "").split(",")

	dependencies = writeDeps("Depends")
	dependencies += writeDeps("Imports")
	dependencies += writeDeps("LinkingTo")
	suggests = writeDeps("Suggests")

	try:
		packageURL = record["URL"].split(",")[0].split(" ")[0].split("\n")[0].replace("\"", "")
	except:
		urlbool = False
	
	if "r-" + package.lower().replace(".","-") in packageVersions.keys():
		if record["Version"] in packageVersions["r-" + package.lower().replace(".","-")] or os.path.isdir("packages/r-" + package.lower().replace(".","-")):
			print(f"({'{:.2f}'.format((progress/total) * 100)}%) Package {'r-' + package.lower().replace('.','-')} already exists")
			return()
		else:
			pass
	else:
		pass

	baseurl_latest = f"https://cran.r-project.org/src/contrib/{package}_{record['Version']}.tar.gz"
	latest = requests.get(baseurl_latest, allow_redirects=True)
	sha256_hash_latest = hashlib.sha256()
	sha256_hash_latest.update(latest.content)
	versions = (f"""	version("{record['Version']}", sha256="{sha256_hash_latest.hexdigest()}")\n""")

	dependencylist = []
	variants = []
	for k in dependencies:
		try:
			dependencylist.append("\tdepends_on(\"" + packageName(k)[0] + packageName(k)[1] + "\", type=(\"build\", \"run\"))\n")
		except:
			continue
	for l in suggests:
		try:
			pkg = packageName(l)
			if pkg[0] == ('r-' + package.lower().replace('.','-')):
				continue
			dependencylist.append("\tdepends_on(\"" + pkg[0] + pkg[1] + "\", when=\"+" + pkg[0] + "\", type=(\"build\", \"run\"))\n")
			variants.append("\tvariant(\"" + pkg[0] + "\", default=" + str(pkg[3]) + ", description=\"Enable " + pkg[0] + " support\")\n")
		except:
			continue

	classname = package.split(".")
	for i in range(len(classname)):
		classname[i] = classname[i].capitalize()
	classname = "".join(classname)

	os.makedirs("packages/r-" + package.lower().replace(".","-"))

	f = open("packages/r-" + package.lower().replace(".","-") + "/package.py", "w")
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
	print(f"({'{:.2f}'.format((progress/total) * 100)}%) Package {'r-' + package.lower().replace('.','-')} has been created!")

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
		elif "==" in version:
			fullname = k.replace(".","-")
			getversion = f"@={version[2:]}"
			type = version[2:]
		else:
			fullname = k.replace(".","-")
			getversion = f"@={version[1:]}"
			type = version[2:]
	else:
		fullname = k.replace(".","-")
		getversion = ""
		type = "any"
	if fullname.lower() == "r":
		return "r", getversion, type, False
	record = pandasDatabase.loc[pandasDatabase['Package'] == k]
	if len(record) == 0:
		if packagesBIOC.get(k) == None:
			raise Exception(f"Package {'r-' + k.lower().replace('.','-')} not found in database")
	# print(fullname.lower(), getversion, type)
	return "r-" + fullname.lower(), getversion, type, False

def packageLoop(lib, libname):
	print(f"Creating {libname} packages...")
	global progress, total
	progress = 0
	total = len(lib)
	for i in lib:
		writePackage(i)
		#time.sleep(1)
	print(f"Finished creating {libname} packages!")


packageLoop(packagesBIOC.keys(), "Bioconductor")
packageLoop(pandasDatabase["Package"], "CRAN")