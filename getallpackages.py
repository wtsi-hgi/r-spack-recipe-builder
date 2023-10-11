import hashlib
import requests
import pyreadr
import pandas as pd
import os
import json
from packaging.version import parse
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
for cranPackage in pandasDatabase["Package"]:
	record = pandasDatabase.loc[pandasDatabase['Package'] == cranPackage]

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

	if "r-" + cranPackage.lower().replace(".","-") in packageVersions.keys():
		if not record["Version"] in packageVersions["r-" + cranPackage.lower().replace(".","-")]:
			#TODO: Add version checking
			continue
	else:
		continue