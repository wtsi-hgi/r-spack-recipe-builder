import hashlib
import shutil
import re
import requests
import pyreadr
import pandas as pd
import os
import json
import pickle
import email.utils, datetime
import subprocess

spackBin = "/opt/spack/bin/spack"

def getRepos():
	cmd = subprocess.run([spackBin, "repo", "list"], capture_output=True)
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
			splitted = i.replace(" ", "").split(",")
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
	f = open("packages/r-" + package.lower().replace(".","-") + "/package.py", "w")
	f.write(f"""{header}

{"".join(versions)}
{"".join(depends)}{footer}""")
	f.close()

class PackageMaker:
	def __init__(self, actualDirs, packageVersions, systemRequirements, missingDependencies):
		self.actualDirs = actualDirs
		self.packageVersions = packageVersions
		self.lib = self.getPackages()
		self.systemRequirements = systemRequirements
		self.missingDependencies = missingDependencies
		self.blacklist = []
		if os.path.isfile("blacklist.txt"):
			with open("blacklist.txt", "r") as f:
				self.blacklist = f.readlines()

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
		
		if not os.path.isdir("packages/r-" + package.lower().replace(".","-")):
			os.makedirs("packages/r-" + package.lower().replace(".","-"))
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
				if "\tdepends_on(" in lines[j] or "\tversion(" in lines[j]:
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
			fullname = k.replace(".","-")
			ver = version.replace('>','').replace('<','').replace('=','')
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
		record = cran.lib.loc[cran.lib['Package'] == k]
		if len(record) == 0:
			if bioc.lib.get(k) == None:
				raise Exception(f"Package {'r-' + k.lower().replace('.','-')} not found in database")
		# print(fullname.lower(), getversion, type)
		return "r-" + fullname.lower(), getversion, type, False

	def writeDeps(self, record, field):
		if record.get(field) == None:
			return []
		elif pd.isna(record[field]):
			return []

		dependencies = record[field].replace(" ","").replace("\n", "").split(",")
		result = []
		for i in dependencies:
			try:
				result.append(self.packageName(i))
			except:
				continue
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
		if version is False:
			with open("blacklist.txt", "a") as f:
				f.write(f"\n{package}")
			print(f"{self.getProgress('x')} {self.packman} package {'r-' + package.lower().replace('.','-')}")
			return

		classname = getClassname(package)
		
		header, footer = self.getTemplate(mode, package, name, description, homepage, classname)

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
		savedDatabase.close()
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
	hashes = {}

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
				fp.close()

		with open('biocLibrary.pkl', 'rb') as fp:
			packagesBIOC = pickle.load(fp)
			fp.close()
		return packagesBIOC


	def packageLoop(self):
		if os.path.exists("BIOCHashes.json"):
			print("Downloading Bioconductor hashes")
			with open("BIOCHashes.json", "r") as f:
				self.hashes = json.load(f)
		else:
			self.hashes = {}
		record = lambda x: self.lib[x]
		super().packageLoop(self.lib.keys(), "Bioconductor", record)
		with open("BIOCHashes.json", "w") as f:
			json.dump(self.hashes, f)
	
	def getURL(self, package, record):
		return f"https://bioconductor.org/packages/release/bioc/src/contrib/{package}_{record['Version']}.tar.gz"

	def getChecksum(self, record, package):
		# return f"""\tversion("{record['Version']}", sha256="TODO_UNCOMMENT")\n"""
		if "git_url" in record.keys():
			if record["git_last_commit"] in self.hashes.keys():
				return f"""\tversion("{record['Version']}", commit="{self.hashes[record["git_last_commit"]]}")\n"""
			gitRefs = requests.get(record['git_url'] + "/info/refs", allow_redirects=True).text.split("\n")
			for i in range(len(gitRefs)):
				hash = gitRefs[i].split("\trefs/heads/")
				if hash[1] == record['git_branch']:
					commitHash = hash[0]
					break
			self.hashes[record["git_last_commit"]] = commitHash
			if len(self.hashes.keys()) % 50 == 0:
				with open("BIOCHashes.json", "w") as f:
					json.dump(self.hashes, f)
			return f"""\tversion("{record['Version']}", commit="{commitHash}")\n"""
		else:
			return False



print("Welcome to the Spack recipe creator for R!\n [+] means a package is freshly created\n [*] means a package is updated\n [~] means a package is already up to date\n [x] means a package won't be created because it is deprecated or it exists in blacklist.txt\n")

actualDirs = getRepos()
packageVersions = getExistingVersions()
systemRequirements = getSystemRequirements()
missingDependencies = getMissingDependencies()


cran = CRANPackageMaker(actualDirs, packageVersions, systemRequirements, missingDependencies)
bioc = BIOCPackageMaker(actualDirs, packageVersions, systemRequirements, missingDependencies)

cran.packageLoop()
bioc.packageLoop()
setSystemRequirements(systemRequirements)