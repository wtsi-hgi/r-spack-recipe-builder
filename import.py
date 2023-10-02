import hashlib
import requests
import pyreadr
import tempfile
import pandas as pd
import os

databaseurl = "https://cran.r-project.org/web/packages/packages.rds"

response = requests.get(databaseurl, allow_redirects=True)
tmpfile = tempfile.NamedTemporaryFile()
tmpfile.write(response.content)

database = pyreadr.read_r(tmpfile.name)
database = database[None]

pandasDatabase = pd.DataFrame(database)

urlbool = True

package = str(input("Please enter package name: "))

if not os.path.exists("r-" + package.lower().replace(".","-")):
    os.makedirs("r-" + package.lower().replace(".","-"))

f = open("r-" + package.lower().replace(".","-") + "/package.py", "w")

record = pandasDatabase.loc[pandasDatabase['Package'] == package]
print(record)

name, description = record["Title"].values[0], record["Description"].values[0]
try:
    dependencies = record["Imports"].values[0].replace(" ","").replace("\n", "").split(",")
except:
    dependencies = []

try:
    packageURL = record["URL"].values[0]
except:
    urlbool = False

f.write("# Copyright 2013-2023 Lawrence Livermore National Security, LLC and other\
\n# Spack Project Developers. See the top-level COPYRIGHT file for details.\
\n#\
\n# SPDX-License-Identifier: (Apache-2.0 OR MIT)\
\n\
\nfrom spack.package import *\
\n\
\n\
\nclass RRvenn(RPackage):\n")

f.write("\t\"\"\"" + name)
f.write("\n\n")
f.write("\t"+description.replace(" \n", "").replace("\n", "").replace("    ", " "))
f.write("\n\t\"\"\"")

f.write("\n\n")
if urlbool and str(packageURL) != "nan":
    f.write(f"\thomepage = \"{packageURL}\"\n")
f.write(f"\tcran = \"{package}\"\n")
f.write("\n")

source = requests.get("https://cran.r-project.org/src/contrib/" + package + "_" + record["Version"].values[0] + ".tar.gz", allow_redirects=True)
sha256_hash = hashlib.sha256()
sha256_hash.update(source.content)
f.write(f"\tversion(\"{record['Version'].values[0]}\", sha256=\"{sha256_hash.hexdigest()}\")\n")

f.write("\n")
for k in dependencies:
    k = "r-" + k
    if "(>=" in k:
        version = k.split("(>=")
        k = version[0].replace(".","-")
        version = version[1].lower().replace(")","")
        k = k + f"@{version}:"
    f.write("\tdepends_on(\"" + k + "\", type=(\"build\", \"run\"))\n")
f.write("\n")

f.close()