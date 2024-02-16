import requests
# currently only lists all pypi dependencies for a given pypi package

dependencies = []
def get_package_dependencies(package_name, package_version):
	url = f"https://libraries.io/api/pypi/{package_name}/{package_version}/dependencies?api_key=ebb39aed4c41baa4c4e8a384a8775cd9"
	response = requests.get(url)
	
	if response.status_code == 200:
		json = response.json()
		for i in json["dependencies"]:
			if str(i["project_name"]).lower() not in dependencies and str(i["platform"]).lower() == "pypi" and i["optional"] == False:
				dependencies.append(str(i["project_name"]).lower())
				print(i["project_name"])
				get_package_dependencies(str(i["project_name"]).lower(), i["latest_stable"])
			else:
				continue
		return list(set(dependencies))
	else:
		return None

# Example usage
package_name = "django"
package_version = "latest"
request = requests.get(f"https://pypi.org/pypi/{package_name}/json")
if request.status_code != 200:
	print(f"Failed to retrieve package {package_name}")
	exit()
python_version = request.json()["info"]["requires_python"]
dependencies.append("python"+python_version)
get_package_dependencies(package_name, package_version)
if dependencies != None:
	print(f"Dependencies for {package_name}:")
	print(dependencies)
else:
	print(f"Failed to retrieve dependencies for {package_name}")