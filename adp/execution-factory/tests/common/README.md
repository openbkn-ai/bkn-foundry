The commmon directory contains some refined announcement functions. Here is an example:

Code without optimization:
```python
# Some functions in a py file in the lib directory.
@allure.step('Cancel the password review')
    def DeletePendingDetail(self, ip, token, apply_id):
        requrl = 'https://%s/api/document/v1/security_classification_approval/pending_detail/%s' % (ip, apply_id)
        dict1 = {}
        dict1['Authorization'] = "Bearer %s" % token
        r = requests.request('delete', requrl, verify=False, headers=dict1)
        allure.attach(r.url, "url", allure.attachment_type.TEXT)
        allure.attach(str(r.status_code), "status_code", allure.attachment_type.TEXT)
        allure.attach(r.content, "content", allure.attachment_type.TEXT)
        if r.status_code == 204:
            return r.status_code
        else:
            return r.status_code, json.loads(r.content)

@allure.step('Set file confidentiality level enumeration')
    def SetConsoleFileClassifications(self, ip, tokenid, body):
        requrl = 'https://%s/api/document/v1/console/file-classifications' % (ip)
        dict1 = {}
        dict1['Authorization'] = "Bearer %s" % tokenid
        body = body
        r = requests.request('PUT', requrl, verify=False, json=body, headers=dict1)
        allure.attach(r.url, "url", allure.attachment_type.TEXT)
        allure.attach(str(r.status_code), "status_code", allure.attachment_type.TEXT)
        allure.attach(r.content, "content", allure.attachment_type.TEXT)
        if r.status_code == 204:
            return r.status_code
        else:
            return r.status_code, json.loads(r.content)

@allure.step('Set system secret level')
    def SetConsoleSystemClassifications(self, ip, tokenid, body):
        requrl = 'https://%s/api/document/v1/console/system-classification' % (ip)
        dict1 = {}
        dict1['Authorization'] = "Bearer %s" % tokenid
        body = body
        r = requests.request('PUT', requrl, verify=False, json=body, headers=dict1)
        allure.attach(r.url, "url", allure.attachment_type.TEXT)
        allure.attach(str(r.status_code), "status_code", allure.attachment_type.TEXT)
        allure.attach(r.content, "content", allure.attachment_type.TEXT)
        if r.status_code == 204:
            return r.status_code
        else:
            return r.status_code, json.loads(r.content)
```
Optimized code:

```python
# A certain py file in the common directory refines some public methods.
@allure.step('API request')
def _make_api_request(self, method, ip, token, endpoint, body=None, path_param=None):
"""Basic API request method.
    
    Args:
method (str): request method (GET, POST, PUT, DELETE, etc.)
ip (str): server IP.
token (str): authentication token.
endpoint (str): API path.
body (dict, optional): request body.
path_param (str, optional): path parameter.
    
    Returns:
int or tuple: status code (204) or tuple of status code and response content.
    """
# Build URL.
    if path_param:
        requrl = f'https://{ip}/api/document/v1/{endpoint}/{path_param}'
    else:
        requrl = f'https://{ip}/api/document/v1/{endpoint}'
    
# Request header.
    headers = {'Authorization': f"Bearer {token}"}
    
# Send request.
    r = requests.request(method, requrl, verify=False, json=body, headers=headers)
    
# Allure report attachments.
    allure.attach(r.url, "url", allure.attachment_type.TEXT)
    allure.attach(str(r.status_code), "status_code", allure.attachment_type.TEXT)
    allure.attach(r.content, "content", allure.attachment_type.TEXT)
    
# Return response.
    if r.status_code == 204:
        return r.status_code
    else:
        return r.status_code, json.loads(r.content)

# Here is how to use it, or put it in a py file in the original lib directory.
@allure.step('Cancel the password review')
def DeletePendingDetail(self, ip, token, apply_id):
    return self._make_api_request(
        method='delete',
        ip=ip,
        token=token,
        endpoint='security_classification_approval/pending_detail',
        path_param=apply_id
    )


@allure.step('Set file confidentiality level enumeration')
def SetConsoleFileClassifications(self, ip, tokenid, body):
    return self._make_api_request(
        method='PUT',
        ip=ip,
        token=tokenid,
        endpoint='console/file-classifications',
        body=body
    )


@allure.step('Set system secret level')
def SetConsoleSystemClassifications(self, ip, tokenid, body):
    return self._make_api_request(
        method='PUT',
        ip=ip,
        token=tokenid,
        endpoint='console/system-classification',
        body=body
    )
```