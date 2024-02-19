import json
import os
import subprocess
import requests
# currently only lists all pypi dependencies for a given pypi package
spackBin = "spack/bin/spack"

def getExistingVersions():
	os.makedirs("packages", exist_ok=True)
	os.makedirs("libs", exist_ok=True)
	print("Fetching package versions, this could take a while...")
	stream = subprocess.run([spackBin, "list", "--format", "version_json", "py-*"], capture_output=True)
	decoded = stream.stdout.decode("utf-8").strip()
	builtin = json.loads(decoded)
	print("Versions successfully fetched!\n")
	packageVersions = {}
	for row in builtin:
		packageVersions[row["name"]] = row["versions"]
	return packageVersions

def addPythonAsDependency(package_name):
	request = requests.get(f"https://pypi.org/pypi/{package_name}/json")
	if request.status_code != 200:
		print(f"Failed to retrieve package {package_name}")
		exit()
	python_version = request.json()["info"]["requires_python"]
	dependencies.append("python"+python_version)

dependencies = []
def get_package_dependencies(package_name, package_version):
	url = f"https://libraries.io/api/pypi/{package_name}/{package_version}/dependencies?api_key=ebb39aed4c41baa4c4e8a384a8775cd9"
	response = requests.get(url)
	if response.status_code != 200:
		return None
	
	json = response.json()
	for i in json["dependencies"]:
		if str(i["project_name"]).lower() not in dependencies and str(i["platform"]).lower() == "pypi" and i["optional"] == False:
			dependencies.append(str(i["project_name"]).lower())
			print(i["project_name"])
			get_package_dependencies(str(i["project_name"]).lower(), i["latest_stable"])
		else:
			continue
	return list(set(dependencies))

def pyify(package):
	return "py-" + package.lower().replace(".","-")

package_name = str(input("Enter package name: "))
package_version = "latest"

addPythonAsDependency(package_name)
existingVersions = getExistingVersions()

get_package_dependencies(package_name, package_version)

# prints the dependencies, will be replaced by a function that writes to a file
if dependencies != None:
	print(f"Dependencies for {package_name}:")
	print(dependencies)
else:
	print(f"Failed to retrieve dependencies for {package_name}")

for i in dependencies:
	if "python>" in i or "python<" in i:
		continue
	pkg = pyify(i)
	if pkg in existingVersions:
		print(f"✅ {pkg} already exists in spack")
	else:
		print(f"❌ {pkg} does not exist in spack")
		# create a package for the missing package