import hashlib
import re
import shutil
import requests
import pyreadr
import pandas as pd
import os
import json
import pickle
import email.utils, datetime
import subprocess

spackBin = "spack/bin/spack"

def getRepos():
	cmd = subprocess.run([spackBin, "repo", "list"], capture_output=True)
	repoDirs = cmd.stdout.decode("utf-8").strip()
	splittedDirs = repoDirs.split("\n")
	actualDirs = []
	for dir in splittedDirs:
		actualDirs.append(dir.split(" ", 1)[1].strip())
	return actualDirs

def getExistingVersions():
	os.makedirs("packages", exist_ok=True)
	os.makedirs("libs", exist_ok=True)
	print("Fetching package versions, this could take a while...")
	stream = subprocess.run([spackBin, "list", "--format", "version_json", "r-*"], capture_output=True)
	decoded = stream.stdout.decode("utf-8").strip()
	builtin = json.loads(decoded)
	print("Versions successfully fetched!\n")
	packageVersions = {}
	for row in builtin:
		packageVersions[row["name"]] = row["versions"]
	return packageVersions

def getSystemRequirements():
	if not os.path.isfile("requirementsDict.tsv"):
		return {}
	else:
		file = open("requirementsDict.tsv", "r")
		lines = file.readlines()
		file.close()
		dict = {}
		for i in lines:
			splitted = i.strip().split("\t")
			if len(splitted) > 1:
				dict[splitted[0]] = splitted[1:]
			else:
				dict[splitted[0]] = []
		return dict

def setSystemRequirements(dict):
	file = open("requirementsDict.tsv", "w")
	for i in dict.keys():
		file.write(f"{i}\t{'	'.join(dict[i])}\n")
	file.close()

def getMissingDependencies():
	if not os.path.isfile("missingDependencies.csv"):
		return {}
	else:
		file = open("missingDependencies.csv", "r")
		lines = file.readlines()
		file.close()
		dict = {}
		for i in lines:
			splitted = list(map(str.strip, i.replace(" ", "").split(",")))
			if len(splitted) > 1:
				dict[splitted[0]] = splitted[1:]
			else:
				dict[splitted[0]] = []
		return dict

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

def writeRecipe(header, footer, versions, depends, package):
	os.makedirs("packages/r-" + package.lower().replace(".","-"), exist_ok=True)
	f = open("packages/r-" + package.lower().replace(".","-") + "/package.py", "w")
	f.write(f"""{header}

{"".join(versions)}
{"".join(depends)}{footer}""")
	f.close()

class PackageMaker:
	packageMakers = []
	comment = ""

	def __init__(self, actualDirs, packageVersions, systemRequirements, missingDependencies):
		self.actualDirs = actualDirs
		self.packageVersions = packageVersions
		self.lib = self.getPackages()
		self.systemRequirements = systemRequirements
		self.missingDependencies = missingDependencies
		self.blacklist = []
		PackageMaker.packageMakers.append(self)
		
		if os.path.isfile("blacklist.txt"):
			with open("blacklist.txt", "r") as f:
				self.blacklist = f.readlines()
				for i in range(len(self.blacklist)):
					self.blacklist[i] = self.blacklist[i].strip()

	def getExistingFiles(self, package, record):
		def pullFiles():
			for dir in actualDirs:
				if os.path.isfile(dir + "/packages/r-" + package.lower().replace(".","-") + "/package.py"):
					location = dir
					break
			if location != os.path.dirname(os.path.realpath(__file__)):
				shutil.copytree(location + "/packages/r-" + package.lower().replace(".","-"), "packages/r-" + package.lower().replace(".","-"))		
			return "*"

		if "r-" + package.lower().replace(".","-") in self.packageVersions.keys():
			if "SystemRequirements" in record.keys():
				if pd.notna(record["SystemRequirements"]):
					return pullFiles()
			if record["Version"] in self.packageVersions["r-" + package.lower().replace(".","-")]:
				print(f"{self.getProgress('~')} {self.packman} package {'r-' + package.lower().replace('.','-')}")
				raise Exception(f"Package {'r-' + package.lower().replace('.','-')} is already up to date")
			return pullFiles()
		
		return "+"

	def getProgress(self, mode):
		self.progress += 1
		spaces = " " * (6 - len(str('{:.2f}'.format((self.progress/self.total) * 100))))
		return f"({'{:.2f}'.format((self.progress/self.total) * 100)}%){spaces}[{mode}]"

	def getDepends(self, dependencies):
		depends_on = []
		dependenciesDict = {}
		dependenciesList = []
		for j in dependencies:
			dependenciesDict[j[0]] = j[1:]
		for k in dependenciesDict.keys():
			dependenciesList.append([k] + list(dependenciesDict[k]))
		for i in dependenciesList:
			depends_on.append("\tdepends_on(\"" + i[0] + i[1] + "\", type=(\"build\", \"run\"))\n")
		return depends_on

	def getTemplate(self, mode, package, name, description, homepage, classname, record):
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
			with open("packages/r-" + package.lower().replace(".","-") + "/package.py", "r") as f:
				lines = f.readlines()
			firstline = 0
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
					for k in range(3):
						if "\")" in lines[j]:
							lastline = j + k + 1
							break
			footer = "".join(lines[lastline:])
		if self.getURL(record) != "" and self.packman == "bioc":
			header += f"\n\turls = [\"{self.getURL(record)}\", \"{self.url}src/contrib/Archive/{package}/{package}_{record['Version']}.tar.gz\"]"
		if self.comment != "":
			footer += f"\n\t# {self.comment}"
		return header, footer


	def packageName(self, k):
		if "(" in k:
			version = k.split("(")
			k = version[0]
			version = version[1].lower().replace(")","")
			getversion = ""
			fullname = k.replace(".","-")
			ver = version.replace('>','').replace('<','').replace('=','')
			ver = re.sub("\.0+([0-9])", ".\\1", ver)
			while ver.endswith(".0"):
				ver = ver[:-2]

			type = ""
			if ">" in version:
				getversion = f"@{ver}:"
				type = "at least "
			elif "<" in version:
				getversion = f"@:{ver}"
			elif "=" in version:
				getversion = f"@{ver}"
			type += ver
		else:
			fullname = k.replace(".","-")
			getversion = ""
			type = "any"
		if fullname.lower() == "r":
			return "r", getversion, type, False
		
		for i in PackageMaker.packageMakers:
			if i.exists(k):
				return "r-" + fullname.lower(), getversion, type, False
		return False

	def writeDeps(self, record, field):
		if record.get(field) == None:
			return []
		elif pd.isna(record[field]):
			return []

		dependencies = record[field].replace(" ","").replace("\n", "").split(",")
		result = []
		for i in dependencies:
			name = self.packageName(i)
			if not name:
				continue
			else:
				result.append(self.packageName(i))
		return result

	def writeRequirements(self, record):
		if record.get("SystemRequirements") == None:
			return []
		elif pd.isna(record["SystemRequirements"]):
			return []
		sysreq = repr(record["SystemRequirements"]).replace("	", " ")
		if sysreq in self.systemRequirements.keys():
			return list(map(lambda x: f"\tdepends_on(\"{x}\", type=(\"build\", \"link\", \"run\"))\n", self.systemRequirements[sysreq]))
		dependencylist = []
		requirements = record["SystemRequirements"].replace("\n", " ").replace(";",",").split(",")
		log = open("requirements.log", "a")
		logThese = []
		dependencyNames = []
		for i in requirements:
			name = i.split("(")[0].strip().lower().replace("\'", "").replace("\"", "")
			if "gnu " in name:
				name = name.replace("gnu ", "")
			if " " in name or name == "":
				logThese = [(f"[manual]    {record['Package']} => {sysreq}\n")]
				dependencylist = []
				dependencyNames = []
				break
			else:
				dependencylist.append(f"\tdepends_on(\"{name}\", type=(\"build\", \"link\", \"run\"))\n")
				logThese.append(f"[automatic] {record['Package']} => {i} [{name}]")
				dependencyNames.append(name.strip())
		log.write('\n'.join(logThese) + "\n")
		if len(sysreq) >= 1:
			self.systemRequirements[sysreq] = dependencyNames
		log.close()
		return dependencylist

	def get(self, package, record):		
		if package in self.blacklist:
			print(f"{self.getProgress('x')} {self.packman} package {'r-' + package.lower().replace('.','-')}")
			return

		try:
			mode = self.getExistingFiles(package, record)
		except:
			return

		name, description = record["Title"], record["Description"].replace("\\", "")

		dependencyList = []
		dependencyList += self.writeDeps(record, "Depends")
		dependencyList += self.writeDeps(record, "Imports")
		dependencyList += self.writeDeps(record, "LinkingTo")

		dependencies = self.getDepends(dependencyList)
		dependencies += self.writeRequirements(record)
		if package in self.missingDependencies.keys():
			for dep in self.missingDependencies[package]:
				dependencies += f"\tdepends_on(\"{dep}\", type=(\"build\", \"link\", \"run\"))\n"

		homepage = getHomepage(record)

		version = self.getChecksum(record, package)
		if version == "":
			print(f"{self.getProgress('x')} {self.packman} package {'r-' + package.lower().replace('.','-')}")
			return

		classname = getClassname(package)
		
		header, footer = self.getTemplate(mode, package, name, description, homepage, classname, record)

		writeRecipe(header, footer, version, dependencies, package)
		print(f"{self.getProgress(mode)} {self.packman} package {'r-' + package.lower().replace('.','-')}")

	def packageLoop(self, lib, libname, record):
		print(f"Creating {libname} packages...")
		self.progress = 0
		self.total = len(lib)
		for i in lib:
			self.get(i, record(i))
		print(f"Finished creating {libname} packages!")

class CRANPackageMaker(PackageMaker):
	packman = "cran"
	cacheFilename = "libs/cranLibrary.rds"
	url = "https://cran.r-project.org/"

	def getPackages(self):
		url = "https://cran.r-project.org/web/packages/packages.rds"
		cranHead = requests.head(url)
		cranWebTime = email.utils.parsedate_to_datetime(cranHead.headers.get('last-modified')).replace(tzinfo=None)
		cranLocalTime = datetime.datetime.fromtimestamp(os.path.getmtime(self.cacheFilename)) if os.path.isfile(self.cacheFilename) else datetime.datetime.fromtimestamp(0)
		if not os.path.isfile(self.cacheFilename) or cranWebTime > cranLocalTime:
			print("Downloading CRAN database")
			response = requests.get(url, allow_redirects=True)
			savedDatabase = open(self.cacheFilename, "wb")
			savedDatabase.write(response.content)
			savedDatabase.close()
		savedDatabase = open(self.cacheFilename, "rb")
		database = pyreadr.read_r(savedDatabase.name)
		savedDatabase.close()
		database = database[None]
		pandasDatabase = pd.DataFrame(database)
		return pandasDatabase
	
	def getURL(self, record):
		return f"https://cran.r-project.org/src/contrib/{record['Package']}_{record['Version']}.tar.gz"
		
	def packageLoop(self):
		record = lambda x: self.lib.loc[self.lib["Package"] == x].to_dict('records')[0]
		super().packageLoop(self.lib["Package"], "CRAN", record)
		
	def getChecksum(self, record, package):
		end = ""
		if package[-1].isdigit():
			end = f', url="{self.getURL(record)}"'
		if "MD5sum" in record.keys():
			return f"""\tversion("{record['Version']}", md5="{record['MD5sum']}"{end})\n"""
		else:
			baseurl = self.getURL(record)
			latest = requests.get(baseurl, allow_redirects=True)
			sha256_hash_latest = hashlib.sha256()
			sha256_hash_latest.update(latest.content)
			return f"""\tversion("{record['Version']}", sha256="{sha256_hash_latest.hexdigest()}"{end})\n"""
		
	def exists(self, k):
		record = self.lib.loc[self.lib['Package'] == k]
		return len(record) > 0

class BIOCPackageMaker(PackageMaker):
	packman = "bioc"
	hashes = {}

	def getPackages(self):
		biocHead = requests.head(self.url + "VIEWS")
		biocWebTime = email.utils.parsedate_to_datetime(biocHead.headers.get('last-modified')).replace(tzinfo=None)
		biocLocalTime = datetime.datetime.fromtimestamp(os.path.getmtime(self.cacheFilename)) if os.path.isfile(self.cacheFilename) else datetime.datetime.fromtimestamp(0)
		if not os.path.isfile(self.cacheFilename) or biocWebTime > biocLocalTime:
			print(f"Downloading {self.name} database")
			response = requests.get(self.url + "VIEWS", allow_redirects=True)
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
			
			with open(self.cacheFilename, "wb") as fp:
				pickle.dump(packagesBIOC, fp)
				fp.close()

		with open(self.cacheFilename, 'rb') as fp:
			packagesBIOC = pickle.load(fp)
			fp.close()
		return packagesBIOC


	def packageLoop(self):
		if os.path.exists("libs/BIOCHashes.json"):
			print("Loading Bioconductor hashes")
			with open("libs/BIOCHashes.json", "r") as f:
				self.hashes = json.load(f)
		else:
			self.hashes = {}
		record = lambda x: self.lib[x]
		super().packageLoop(self.lib.keys(), self.name, record)
		with open("libs/BIOCHashes.json", "w") as f:
			json.dump(self.hashes, f)

	def getURL(self, record):
		if "source.ver" not in record.keys():
			return ""
		return self.url + record['source.ver']

	def getChecksum(self, record, package):
		end = ""
		if package[-1].isdigit():
			end = f', url="{self.getURL(record)}"'
		if "source.ver" in record.keys():
			return f"""\tversion("{record['Version']}", md5="{record['MD5sum']}"{end})\n"""
		if "git_url" in record.keys():
			if record["git_last_commit"] in self.hashes.keys():
				return f"""\tversion("{record['Version']}", commit="{self.hashes[record["git_last_commit"]]}"{end})\n"""
			gitRefs = requests.get(record['git_url'] + "/info/refs", allow_redirects=True).text.split("\n")
			for i in range(len(gitRefs)):
				hash = gitRefs[i].split("\trefs/heads/")
				if hash[1] == record['git_branch']:
					commitHash = hash[0]
					break
			self.hashes[record["git_last_commit"]] = commitHash
			if len(self.hashes.keys()) % 50 == 0:
				with open("libs/BIOCHashes.json", "w") as f:
					json.dump(self.hashes, f)
			return f"""\tversion("{record['Version']}", commit="{commitHash}"{end})\n"""
		else:
			return ""
		
	def exists(self, k):
		return self.lib.get(k) is not None

class BIOCSoftware(BIOCPackageMaker):
	url = "https://www.bioconductor.org/packages/release/bioc/"
	cacheFilename = "libs/biocLibrary.pkl"
	name = "Bioconductor software"
	comment = ""

class BIOCAnnotations(BIOCPackageMaker):
	url = "https://www.bioconductor.org/packages/release/data/annotation/"
	cacheFilename = "libs/biocAnnotationLibrary.pkl"
	name = "Bioconductor annotations"
	comment = "annotation"

class BIOCExperiments(BIOCPackageMaker):
	url = "https://www.bioconductor.org/packages/release/data/experiment/"
	cacheFilename = "libs/biocExperimentLibrary.pkl"
	name = "Bioconductor experiments"
	comment = "experiment"

class BIOCWorkflows(BIOCPackageMaker):
	url = "https://www.bioconductor.org/packages/release/workflows/"
	cacheFilename = "libs/biocWorkflowLibrary.pkl"
	name = "Bioconductor workflows"
	comment = "workflow"

print("Welcome to the Spack recipe creator for R!\n [+] means a package is freshly created\n [*] means a package is updated\n [~] means a package is already up to date\n [x] means a package can't be created or is blacklisted\n")

actualDirs = getRepos()
packageVersions = getExistingVersions()
systemRequirements = getSystemRequirements()
missingDependencies = getMissingDependencies()

managers = (
	BIOCSoftware, 
	BIOCAnnotations, 
	BIOCExperiments, 
	BIOCWorkflows,
	CRANPackageMaker, 
)

for p in managers:
	p(actualDirs, packageVersions, systemRequirements, missingDependencies)

for p in PackageMaker.packageMakers:
	p.packageLoop()

setSystemRequirements(systemRequirements)