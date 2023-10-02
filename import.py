import hashlib
import requests
import pyreadr
import tempfile
import pandas as pd

databaseurl = "https://cran.r-project.org/web/packages/packages.rds"

response = requests.get(databaseurl, allow_redirects=True)
tmpfile = tempfile.NamedTemporaryFile()
tmpfile.write(response.content)

database = pyreadr.read_r(tmpfile.name)
database = database[None]

pandasDatabase = pd.DataFrame(database)

urlbool = True

package = str(input("Please enter package name: "))

record = pandasDatabase.loc[pandasDatabase['Package'] == package]

name, description = record["Title"].values[0], record["Description"].values[0]
try:
    dependencies = record["Imports"].values[0].split(", ")
except:
    dependencies = []

try:
    packageURL = record["URL"].values[0]
except:
    urlbool = False

print("\"\"\"" + name)
print()
print(description)
print("\"\"\"")

print()
if urlbool:
    print(f"homepage = \"{packageURL}\"")
print(f"cran = \"{package}\"")
print()

source = requests.get("https://cran.r-project.org/src/contrib/" + package + "_" + record["Version"].values[0] + ".tar.gz", allow_redirects=True)
sha256_hash = hashlib.sha256()
sha256_hash.update(source.content)
print(f"version(\"{record['Version'].values[0]}\", sha256=\"{sha256_hash.hexdigest()}\")")

print()
for k in dependencies:
    print("depends_on(\"r-" + k.lower().replace(".","-") + "\", type=(\"build\", \"run\"))")
print()

