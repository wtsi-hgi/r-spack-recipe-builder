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

stream = os.popen("singularity run /opt/spack.sif list --format name_only r-*")
builtin = stream.read()
stream.close()
builtin = builtin.split("\n")[:-1]

def importPackage(package):
    
    urlbool = True

    record = pandasDatabase.loc[pandasDatabase['Package'] == package]

    try:
        name, description = record["Title"].values[0], record["Description"].values[0]
    except:
        raise Exception(f"Package {'r-' + package.lower().replace('.','-')} not found in database")
        
    try:
        dependencies = record["Imports"].values[0].replace(" ","").replace("\n", "").split(",")
    except:
        dependencies = []

    try:
        packageURL = record["URL"].values[0]
    except:
        urlbool = False

    if "r-" + package.lower().replace(".","-") in builtin:
        return(f"Package {'r-' + package.lower().replace('.','-')} already exists")
    os.makedirs("packages/r-" + package.lower().replace(".","-"))

    f = open("packages/r-" + package.lower().replace(".","-") + "/package.py", "w")

    f.write(f"# Copyright 2013-2023 Lawrence Livermore National Security, LLC and other\
    \n# Spack Project Developers. See the top-level COPYRIGHT file for details.\
    \n#\
    \n# SPDX-License-Identifier: (Apache-2.0 OR MIT)\
    \n\
    \nfrom spack.package import *\
    \n\
    \n\
    \nclass R{package.replace('.', '').title()}(RPackage):\n")

    f.write("\t\"\"\"" + name)
    f.write("\n\n")
    f.write("\t"+description)
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
        if "(" in k:
            version = k.split("(")
            k = version[0]
            version = version[1].lower().replace(")","")
            try:
                importPackage(k)
            except:
                continue
            if ">=" in version:
                k = k.replace(".","-") + f"@{version[2:]}:"
            elif "<=" in version:
                k = k.replace(".","-") + f"@:{version[2:]}"
        else:
            try:
                importPackage(k)
            except:
                continue
            k = k.replace(".","-")
        k = "r-" + k.lower()
        f.write("\tdepends_on(\"" + k + "\", type=(\"build\", \"run\"))\n")
    f.write("\n")

    f.close()
    print(f"Package {'r-' + package.lower().replace('.','-')} has been created!")


package = str(input("Please enter package name: "))
importPackage(package)