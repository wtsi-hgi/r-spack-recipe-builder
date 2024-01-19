
import email.utils, datetime
import os
import pickle
import pandas as pd
import pyreadr
import requests

class PackageMaker:
	packageMakers = []
	comment = ""

	def __init__(self):
		self.lib = self.getPackages()
		PackageMaker.packageMakers.append(self)
		
		if os.path.isfile("blacklist.txt"):
			with open("blacklist.txt", "r") as f:
				self.blacklist = f.readlines()
				for i in range(len(self.blacklist)):
					self.blacklist[i] = self.blacklist[i].strip()
	
	def packageLoop(self, lib, libname, record):
		topLevel = []
		total = len(lib)
		counter = 0
		for i in lib:
			counter += 1
			print (f"{counter}/{total} {libname} packages", end="\r")
			if self.get(record(i), libname):
				topLevel.append(i)

		print(f"{len(topLevel)}/{total} {libname} packages: {topLevel}\n\n", end="\r")


	def get(self, record, libname):
		if libname == "CRAN":
			if pd.isna(record.get("Reverse depends")):
				if pd.isna(record.get("Reverse suggests")):
					if pd.isna(record.get("Reverse imports")):
						return True
			return False
		else:
			if record.get("dependsOnMe") is None:
				if record.get("importsMe") is None:
					if record.get("suggestsMe") is None:
						return True
			return False


class CRANPackageMaker(PackageMaker):
	packman = "cran"
	cacheFilename = "libs/cranLibrary.rds"

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
	
	def packageLoop(self):
		record = lambda x: self.lib.loc[self.lib["Package"] == x].to_dict('records')[0]
		super().packageLoop(self.lib["Package"], "CRAN", record)


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
		record = lambda x: self.lib[x]
		super().packageLoop(self.lib.keys(), self.name, record)

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

managers = (
	CRANPackageMaker, 
	BIOCSoftware, 
	BIOCAnnotations, 
	BIOCExperiments, 
	BIOCWorkflows,
)

for p in managers:
	p()

for p in PackageMaker.packageMakers:
	p.packageLoop()