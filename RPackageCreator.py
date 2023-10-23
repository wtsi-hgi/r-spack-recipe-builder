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

def getRepos():
	cmd = subprocess.run(["/opt/spack/bin/spack", "repo", "list"], capture_output=True)
	repoDirs = cmd.stdout.decode("utf-8").strip()
	splittedDirs = repoDirs.split("\n")
	actualDirs = []
	for dir in splittedDirs:
		actualDirs.append(dir.split(" ", 1)[1].strip())
	return actualDirs

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

def getClassname(package):
	classname = package.split(".")
	for i in range(len(classname)):
		classname[i] = classname[i].capitalize()
	return "".join(classname)

def getHomepage(record):
	try:
		return record["URL"].split(",")[0].split(" ")[0].split("\n")[0].replace("\"", "")
	except:
		return ""

def writeRecipe(header, footer, versions, depends, variants, package):
	f = open("packages/r-" + package.lower().replace(".","-") + "/package.py", "w")
	f.write(f"""{header}

{"".join(versions)}
{"".join(depends)}
{"".join(variants)}{footer}""")
	f.close()

class PackageMaker:
	def __init__(self, actualDirs, packageVersions):
		self.actualDirs = actualDirs
		self.packageVersions = packageVersions
		self.lib = self.getPackages()

	def getExistingFiles(self, package, record):
		mode = "+"

		if "r-" + package.lower().replace(".","-") in self.packageVersions.keys():
			if record["Version"] in self.packageVersions["r-" + package.lower().replace(".","-")]:
				
				print(f"{self.getProgress('~')} {self.packman} package {'r-' + package.lower().replace('.','-')}")
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

	def getProgress(self, mode):
		self.progress += 1
		spaces = " " * (6 - len(str('{:.2f}'.format((self.progress/self.total) * 100))))
		return f"({'{:.2f}'.format((self.progress/self.total) * 100)}%){spaces}[{mode}]"

	def getDepends(self, dependencies):
		dependencylist = []
		for pkg in dependencies:
			try:
				name = self.packageName(pkg)
				dependencylist.append("\tdepends_on(\"" + name[0] + name[1] + "\", type=(\"build\", \"run\"))\n")
			except:
				continue
		return dependencylist

	def getSuggests(self, suggests, package):
		dependencylist = []
		variants = []
		for i in suggests:
			try:
				pkg = self.packageName(i)
				if pkg[0] == ('r-' + package.lower().replace('.','-')):
					continue
				dependencylist.append("\tdepends_on(\"" + pkg[0] + pkg[1] + "\", when=\"+" + pkg[0] + "\", type=(\"build\", \"run\"))\n")
				variants.append("\tvariant(\"" + pkg[0] + "\", default=" + str(pkg[3]) + ", description=\"Enable " + pkg[0] + " support\")\n")
			except:
				continue
		return dependencylist, variants 

	def getTemplate(self, mode, package, name, description, homepage, classname):
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
	
	{f'homepage = "{homepage}"{backslashN}' if homepage != "" else ''}{self.packman} = "{package}" """
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


	def packageName(self, k):
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
		record = cran.lib.loc[cran.lib['Package'] == k]
		if len(record) == 0:
			if bioc.lib.get(k) == None:
				raise Exception(f"Package {'r-' + k.lower().replace('.','-')} not found in database")
		# print(fullname.lower(), getversion, type)
		return "r-" + fullname.lower(), getversion, type, False

	def writeDeps(self, record, field, package):
		if record.get(field) == None:
			return [], []
		elif pd.isna(record[field]):
			return [], []
		dependencies = record[field].replace(" ","").replace("\n", "").split(",")
		if field == "Suggests":
			return self.getSuggests(dependencies, package)
		else:
			return self.getDepends(dependencies), []

	def get(self, package, record):		
		name, description = record["Title"], record["Description"].replace("\\", "")

		dependencies = []
		dependencies = self.writeDeps(record, "Depends", package)[0]
		dependencies += self.writeDeps(record, "Imports", package)[0]
		dependencies += self.writeDeps(record, "LinkingTo", package)[0]
		x, suggests = self.writeDeps(record, "Suggests", package)
		dependencies += x
		

		homepage = getHomepage(record)

		try:
			mode = self.getExistingFiles(package, record)
		except:
			return

		version = self.getChecksum(record, package)

		classname = getClassname(package)
		
		header, footer = self.getTemplate(mode, package, name, description, homepage, classname)

		writeRecipe(header, footer, version, dependencies, suggests, package)
		print(f"{self.getProgress(mode)} {self.packman} package {'r-' + package.lower().replace('.','-')}")

	def packageLoop(self, lib, libname, record):
		print(f"Creating {libname} packages...")
		time.sleep(2)
		self.progress = 0
		self.total = len(lib)
		for i in lib:
			self.get(i, record(i))
		print(f"Finished creating {libname} packages!")
		time.sleep(2)

class CRANPackageMaker(PackageMaker):
	packman = "cran"

	def getPackages(self):
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
	
	def getURL(self, package, record):
		return f"https://cran.r-project.org/src/contrib/{package}_{record['Version']}.tar.gz"
		
	def packageLoop(self):
		record = lambda x: self.lib.loc[self.lib["Package"] == x].to_dict('records')[0]
		super().packageLoop(self.lib["Package"], "CRAN", record)
		
	def getChecksum(self, record, package):
		if "MD5sum" in record.keys():
			return f"""\tversion("{record['Version']}", md5="{record['MD5sum']}")\n"""
		else:
			baseurl = self.getURL(package, record)
			latest = requests.get(baseurl, allow_redirects=True)
			sha256_hash_latest = hashlib.sha256()
			sha256_hash_latest.update(latest.content)
			return f"""\tversion("{record['Version']}", sha256="{sha256_hash_latest.hexdigest()}")\n"""
		
class BIOCPackageMaker(PackageMaker):
	packman = "bioc"

	def getRecord(self, package):
		return self.packages[package]

	def getPackages(self):
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


	def packageLoop(self):
		record = lambda x: self.lib[x]
		super().packageLoop(self.lib.keys(), "Bioconductor", record)
	
	def getURL(self, package, record):
		return f"https://bioconductor.org/packages/release/bioc/src/contrib/{package}_{record['Version']}.tar.gz"

	def getChecksum(self, record, package):
		if "git_url" in record.keys():
			gitRefs = requests.get(record['git_url'] + "/info/refs", allow_redirects=True).text.split("\n")
			for i in range(len(gitRefs)):
				hash = gitRefs[i].split("\trefs/heads/")
				if hash[1] == record['git_branch']:
					commitHash = hash[0]
					break
			return f"""\tversion("{record['Version']}", commit="{commitHash}")\n"""
		else:
			baseurl = self.getURL(package, record)
			latest = requests.get(baseurl, allow_redirects=True)
			sha256_hash_latest = hashlib.sha256()
			sha256_hash_latest.update(latest.content)
			return f"""\tversion("{record['Version']}", sha256="{sha256_hash_latest.hexdigest()}")\n"""



print("Welcome to the Spack recipe creator for R!\n [+] means a package is freshly created\n [*] means a package is updated\n [~] means a package is already up to date\n")

actualDirs = getRepos()
packageVersions = getExistingVersions()

cran = CRANPackageMaker(actualDirs, packageVersions)
bioc = BIOCPackageMaker(actualDirs, packageVersions)

bioc.packageLoop()
cran.packageLoop()