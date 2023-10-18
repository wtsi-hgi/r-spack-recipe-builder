import hashlib
import shutil
import time
import requests
import pyreadr
import pandas as pd
import os
import json
import pickle
import email.utils, datetime
import subprocess

print("Welcome to the Spack recipe creator for R!\n [+] means a package is freshly created\n [*] means a package is updated\n [~] means a package is already up to date\n")
# Get all repos
cmd = subprocess.run(["/opt/spack/bin/spack", "repo", "list"], capture_output=True)
repoDirs = cmd.stdout.decode("utf-8").strip()
splittedDirs = repoDirs.split("\n")
actualDirs = []
for i in splittedDirs:
	actualDirs.append(i.split(" ", 1)[1].strip())

#Makes CRAN dataframe
databaseurl = "https://cran.r-project.org/web/packages/packages.rds"
cranHead = requests.head(databaseurl)
cranWebTime = email.utils.parsedate_to_datetime(cranHead.headers.get('last-modified')).replace(tzinfo=None)
cranLocalTime = datetime.datetime.fromtimestamp(os.path.getmtime("cranLibrary.rds")) if os.path.isfile("cranLibrary.rds") else datetime.datetime.fromtimestamp(0)
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
biocURL = "https://www.bioconductor.org/packages/release/bioc/VIEWS"
biocHead = requests.head(databaseurl)
biocWebTime = email.utils.parsedate_to_datetime(biocHead.headers.get('last-modified')).replace(tzinfo=None)
biocLocalTime = datetime.datetime.fromtimestamp(os.path.getmtime("biocLibrary.pkl")) if os.path.isfile("biocLibrary.pkl") else datetime.datetime.fromtimestamp(0)
if not os.path.isfile("biocLibrary.pkl") or biocWebTime > biocLocalTime:
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

if not os.path.isdir("packages"):
	os.mkdir("packages")

print("Fetching package versions, this could take a while...")
stream = os.popen("/opt/spack/bin/spack list --format version_json r-*")
builtin = stream.read()
stream.close()
builtin = json.loads(builtin)
print("Versions successfully fetched!\n")
packageVersions = {}
for i in builtin:
	packageVersions[i["name"]] = i["versions"]

progress = 0
total = 0

def writePackage(package):
	global progress, total
	progress += 1
	mode = "+"
	location = "ERROR"
	spaces = " " * (6 - len(str('{:.2f}'.format((progress/total) * 100))))

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
		if record["Version"] in packageVersions["r-" + package.lower().replace(".","-")]:
			
			print(f"({'{:.2f}'.format((progress/total) * 100)}%){spaces}[~] {packman} package {'r-' + package.lower().replace('.','-')}")
			return
		else:
			mode = "*"
			for i in actualDirs:
				if os.path.isfile(i + "/packages/r-" + package.lower().replace(".","-") + "/package.py"):
					location = i
					break
			if location != os.path.dirname(os.path.realpath(__file__)):
				shutil.copytree(location + "/packages/r-" + package.lower().replace(".","-"), "packages/r-" + package.lower().replace(".","-"))
	if not os.path.isdir("packages/r-" + package.lower().replace(".","-")):
		os.makedirs("packages/r-" + package.lower().replace(".","-"))

	if "MD5sum" in record.keys():
		versions = f"""\tversion("{record['Version']}", md5="{record['MD5sum']}")\n"""
	else:
		if packman == "cran":
			baseurl = f"https://cran.r-project.org/src/contrib/{package}_{record['Version']}.tar.gz"
		elif packman == "bioc":
			baseurl = f"https://bioconductor.org/packages/release/bioc/src/contrib/{package}_{record['Version']}.tar.gz"
		latest = requests.get(baseurl, allow_redirects=True)
		sha256_hash_latest = hashlib.sha256()
		sha256_hash_latest.update(latest.content)
		versions = f"""\tversion("{record['Version']}", sha256="{sha256_hash_latest.hexdigest()}")\n"""
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

	if mode == "+":
		backslashN = "\n\t"
		header = f"""# Copyright 2013-2023 Lawrence Livermore National Security, LLC and other
# Spack Project Developers. See the top-level COPYRIGHT file for details.
#
# SPDX-License-Identifier: (Apache-2.0 OR MIT)
	
from spack.package import *
	
			
class R{classname}(RPackage):
	\"\"\"{name}

	{description}
	\"\"\"
	
	{f'homepage = "{packageURL}"{backslashN}' if urlbool and str(packageURL) != "nan" else ''}{packman} = "{package}" """
		footer = ""
	else:
		f = open("packages/r-" + package.lower().replace(".","-") + "/package.py", "r")
		lines = f.readlines()
		firstline = 0
		for i in range(len(lines)):
			lines[i] = lines[i].replace("    ", "\t")
			if "\tversion(" in lines[i]:
				firstline = i
				break
		header = "".join(lines[:firstline]).strip()
		lastline = len(lines)
		for j in range(len(lines)):
			lines[j] = lines[j].replace("    ", "\t")
			if "\tdepends_on(" in lines[j] or "\tvariant(" in lines[j] or "\tversion(" in lines[j]:
				for k in range(3):
					if "\")" in lines[j]:
						lastline = j + k + 1
						break
		footer = "".join(lines[lastline:])

	writeRecipe(header, footer, versions, dependencylist, variants, package)
	print(f"({'{:.2f}'.format((progress/total) * 100)}%){spaces}[{mode}] {packman} package {'r-' + package.lower().replace('.','-')}")

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
	time.sleep(2)
	global progress, total
	progress = 0
	total = len(lib)
	for i in lib:
		writePackage(i)
	print(f"Finished creating {libname} packages!")
	time.sleep(2)

def writeRecipe(header, footer, versions, depends, variants, package):
	f = open("packages/r-" + package.lower().replace(".","-") + "/package.py", "w")
	f.write(f"""{header}

{"".join(versions)}
{"".join(depends)}
{"".join(variants)}{footer}""")
	f.close()


packageLoop(pandasDatabase["Package"], "CRAN")
packageLoop(packagesBIOC.keys(), "Bioconductor")