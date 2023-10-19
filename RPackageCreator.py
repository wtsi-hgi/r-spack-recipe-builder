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

progress = 0
total = 0

print("Welcome to the Spack recipe creator for R!\n [+] means a package is freshly created\n [*] means a package is updated\n [~] means a package is already up to date\n")

def getRepos():
	cmd = subprocess.run(["/opt/spack/bin/spack", "repo", "list"], capture_output=True)
	repoDirs = cmd.stdout.decode("utf-8").strip()
	splittedDirs = repoDirs.split("\n")
	actualDirs = []
	for dir in splittedDirs:
		actualDirs.append(dir.split(" ", 1)[1].strip())
	return actualDirs

def cranGet():
	url = "https://cran.r-project.org/web/packages/packages.rds"
	cranHead = requests.head(url)
	cranWebTime = email.utils.parsedate_to_datetime(cranHead.headers.get('last-modified')).replace(tzinfo=None)
	cranLocalTime = datetime.datetime.fromtimestamp(os.path.getmtime("cranLibrary.rds")) if os.path.isfile("cranLibrary.rds") else datetime.datetime.fromtimestamp(0)
	if not os.path.isfile("cranLibrary.rds") or cranWebTime > cranLocalTime:
		print("Downloading CRAN database")
		response = requests.get(url, allow_redirects=True)
		savedDatabase = open("cranLibrary.rds", "wb")
		savedDatabase.write(response.content)
		savedDatabase.close()
	savedDatabase = open("cranLibrary.rds", "rb")
	database = pyreadr.read_r(savedDatabase.name)
	database = database[None]
	pandasDatabase = pd.DataFrame(database)
	return pandasDatabase

def biocGet():
	url = "https://www.bioconductor.org/packages/release/bioc/VIEWS"
	biocHead = requests.head(url)
	biocWebTime = email.utils.parsedate_to_datetime(biocHead.headers.get('last-modified')).replace(tzinfo=None)
	biocLocalTime = datetime.datetime.fromtimestamp(os.path.getmtime("biocLibrary.pkl")) if os.path.isfile("biocLibrary.pkl") else datetime.datetime.fromtimestamp(0)
	if not os.path.isfile("biocLibrary.pkl") or biocWebTime > biocLocalTime:
		print("Downloading Bioconductor database")
		response = requests.get(url, allow_redirects=True)
		databaseBIOC = (response.text).split("\n\n")
		packagesBIOC = {}
		for package in range(len(databaseBIOC) - 1):
			databaseBIOC[package] = databaseBIOC[package].replace("\n        ", " ")
			databaseBIOC[package] = databaseBIOC[package].split("\n")
			newPackage = {}
			for field in range(len(databaseBIOC[package])):
				databaseBIOC[package][field] = databaseBIOC[package][field].split(": ", 1)
				newPackage[databaseBIOC[package][field][0]] = databaseBIOC[package][field][1]
			packagesBIOC[newPackage["Package"]] = newPackage
		
		with open("biocLibrary.pkl", "wb") as fp:
			pickle.dump(packagesBIOC, fp)

	with open('biocLibrary.pkl', 'rb') as fp:
		packagesBIOC = pickle.load(fp)
	return packagesBIOC

def getExistingVersions():
	if not os.path.isdir("packages"):
		os.mkdir("packages")

	print("Fetching package versions, this could take a while...")
	stream = os.popen("/opt/spack/bin/spack list --format version_json r-*")
	builtin = stream.read()
	stream.close()
	builtin = json.loads(builtin)
	print("Versions successfully fetched!\n")
	packageVersions = {}
	for row in builtin:
		packageVersions[row["name"]] = row["versions"]
	return packageVersions

def whichPackMan(package):
	bioc = biocGet()
	cran = cranGet()
	record = cran.loc[cran['Package'] == package]
	if len(record) == 0:
		if bioc.get(package) == None:
			raise Exception(f"Package {'r-' + package.lower().replace('.','-')} not found in database")
		else:
			record = bioc[package]
			return record, "bioc"
	else:
		record = record.to_dict('records')[0]
		return record, "cran"

def writeDeps(record, field, package):
	if record.get(field) == None:
		return []
	elif pd.isna(record[field]):
		return []
	dependencies = record[field].replace(" ","").replace("\n", "").split(",")
	if field == "Suggests":
		return getSuggests(dependencies, package)
	else:
		return getDepends(dependencies), []

def getURL(record):
	try:
		return record["URL"].split(",")[0].split(" ")[0].split("\n")[0].replace("\"", ""), True
	except:
		return "", False

def getExistingFiles(package, record, packman, packageVersions):
	actualDirs = getRepos()
	mode = "+"

	if "r-" + package.lower().replace(".","-") in packageVersions.keys():
		if record["Version"] in packageVersions["r-" + package.lower().replace(".","-")]:
			
			print(f"{getProgress('~')} {packman} package {'r-' + package.lower().replace('.','-')}")
			raise Exception(f"Package {'r-' + package.lower().replace('.','-')} is already up to date")
		else:
			mode = "*"
			for dir in actualDirs:
				if os.path.isfile(dir + "/packages/r-" + package.lower().replace(".","-") + "/package.py"):
					location = dir
					break
			if location != os.path.dirname(os.path.realpath(__file__)):
				shutil.copytree(location + "/packages/r-" + package.lower().replace(".","-"), "packages/r-" + package.lower().replace(".","-"))
	if not os.path.isdir("packages/r-" + package.lower().replace(".","-")):
		os.makedirs("packages/r-" + package.lower().replace(".","-"))
	return mode

def getProgress(mode):
	global progress, total
	progress += 1
	spaces = " " * (6 - len(str('{:.2f}'.format((progress/total) * 100))))
	return f"({'{:.2f}'.format((progress/total) * 100)}%){spaces}[{mode}]"

def getChecksum(record, packman, package):
	if "MD5sum" in record.keys():
		return f"""\tversion("{record['Version']}", md5="{record['MD5sum']}")\n"""
	else:
		if packman == "cran":
			baseurl = f"https://cran.r-project.org/src/contrib/{package}_{record['Version']}.tar.gz"
		elif packman == "bioc":
			baseurl = f"https://bioconductor.org/packages/release/bioc/src/contrib/{package}_{record['Version']}.tar.gz"
		latest = requests.get(baseurl, allow_redirects=True)
		sha256_hash_latest = hashlib.sha256()
		sha256_hash_latest.update(latest.content)
		return f"""\tversion("{record['Version']}", sha256="{sha256_hash_latest.hexdigest()}")\n"""

def getDepends(dependencies):
	dependencylist = []
	for pkg in dependencies:
		try:
			dependencylist.append("\tdepends_on(\"" + packageName(pkg)[0] + packageName(pkg)[1] + "\", type=(\"build\", \"run\"))\n")
		except:
			continue
	return dependencylist

def getSuggests(suggests, package):
	dependencylist = []
	variants = []
	for pkg in suggests:
		try:
			pkg = packageName(pkg)
			if pkg[0] == ('r-' + package.lower().replace('.','-')):
				continue
			dependencylist.append("\tdepends_on(\"" + pkg[0] + pkg[1] + "\", when=\"+" + pkg[0] + "\", type=(\"build\", \"run\"))\n")
			variants.append("\tvariant(\"" + pkg[0] + "\", default=" + str(pkg[3]) + ", description=\"Enable " + pkg[0] + " support\")\n")
		except:
			continue
	return dependencylist, variants 

def getClassname(package):
	classname = package.split(".")
	for i in range(len(classname)):
		classname[i] = classname[i].capitalize()
	return "".join(classname)

def getTemplate(mode, package, packman, name, description, url, urlbool, classname):
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
	
	{f'homepage = "{url}"{backslashN}' if urlbool and str(url) != "nan" else ''}{packman} = "{package}" """
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

	return header, footer

def writePackage(package):
	packageVersions = getExistingVersions()
	spaces = " " * (6 - len(str('{:.2f}'.format((progress/total) * 100))))

	record, packman = whichPackMan(package)
	
	name, description = record["Title"], record["Description"].replace("\\", "")

	dependencies = writeDeps(record, "Depends")
	dependencies += writeDeps(record, "Imports")
	dependencies += writeDeps(record, "LinkingTo")
	x, suggests = writeDeps(record, "Suggests", package)
	dependencies += x
	

	url, urlbool = getURL(record)

	try:
		mode = getExistingFiles(package, record, packman, packageVersions)
	except:
		return

	version = getChecksum(record, packman, package)

	classname = getClassname(package)
	
	header, footer = getTemplate(mode, package, packman, name, description, url, urlbool, classname)

	writeRecipe(header, footer, version, dependencies, suggests, package)
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
	record = cranGet().loc[cranGet()['Package'] == k]
	if len(record) == 0:
		if biocGet().get(k) == None:
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


packageLoop(cranGet()["Package"], "CRAN")
packageLoop(biocGet().keys(), "Bioconductor")