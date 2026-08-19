# -*- coding:UTF-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import requests
import allure

from urllib3 import disable_warnings
from urllib3.exceptions import InsecureRequestWarning
disable_warnings(InsecureRequestWarning)

class Request():
    def query(self, url, params, headers):
        '''Encapsulate the get query API.'''
        allure.attach(url, name="Request URL")
        allure.attach(str(params), name="Request params")

        resp = requests.get(url, params=params, headers=headers, verify=False, allow_redirects=False)
        # print(resp.url)
        # print(resp.status_code, resp.text)
        # import pdb; pdb.set_trace();
        allure.attach(str(resp.status_code), name="Response Code")
        allure.attach(resp.text, name="Response Result")

        try:
            return [resp.status_code, resp.json()]
        except:
            # If the response is not valid JSON, return the original text.
            return [resp.status_code, resp.text]

    def get(self, url, headers):
        '''Encapsulate the get API.'''
        allure.attach(url, name="Request URL")

        resp = requests.get(url, headers=headers, verify=False, allow_redirects=False)
        # print(url)
        # print(resp.text)
        # import pdb; pdb.set_trace();
        allure.attach(str(resp.status_code), name="Response Code")
        allure.attach(resp.text, name="Response Result")

        try:
            return [resp.status_code, resp.json()]
        except:
            # If the response is not valid JSON, return the original text.
            return [resp.status_code, resp.text]

    def post(self, url, data, headers):
        '''Encapsulate post API.'''
        allure.attach(url, name="Request URL")
        allure.attach(str(data), name="Request Body")
        # print(url)
        resp = requests.post(url, json=data, headers=headers, verify=False, allow_redirects=False)
        # print(resp.status_code, resp.text)

        allure.attach(str(resp.status_code), name="Response Code")
        allure.attach(resp.text, name="Response Result")

        if resp.text == "":
            return [resp.status_code, resp.text]
        else:
            try:
                return [resp.status_code, resp.json()]
            except:
                # If the response is not valid JSON, return the original text.
                return [resp.status_code, resp.text]

    def query_post(self, url, params, data, headers):
        '''Encapsulate the post API with query parameters.'''
        allure.attach(url, name="Request URL")
        allure.attach(str(params), name="Request Params")
        allure.attach(str(data), name="Request Body")
        
        resp = requests.post(url, params=params, json=data, headers=headers, verify=False, allow_redirects=False)

        allure.attach(str(resp.status_code), name="Response Code")
        allure.attach(resp.text, name="Response Result")

        if resp.text == "":
            return [resp.status_code, resp.text]
        else:
            try:
                return [resp.status_code, resp.json()]
            except:
                # If the response is not valid JSON, return the original text.
                return [resp.status_code, resp.text]

    def post_multipart(self, url, files, data, headers, params=None):
        '''Encapsulates the POST API that supports Multipart.'''
        allure.attach(url, name="Request URL")
        
        # Deep copy and clean headers to prevent Content-Type conflicts.
        request_headers = headers.copy()
        if "Content-Type" in request_headers:
            del request_headers["Content-Type"]
            
        if data:
            allure.attach(str(data), name="Form Fields")
        if params:
            allure.attach(str(params), name="Query Params")
            
        resp = requests.post(url, files=files, data=data, headers=request_headers, params=params, verify=False, allow_redirects=False)
        # print(resp.status_code, resp.text)
        
        if resp.status_code == 500:
            print(f"DEBUG: 500 Error Response Body: {resp.text}")

        allure.attach(str(resp.status_code), name="Response Code")
        allure.attach(resp.text, name="Response Result")

        if resp.text == "":
            return [resp.status_code, resp.text]
        else:
            try:
                return [resp.status_code, resp.json()]
            except:
                # If the response is not valid JSON, return the original text.
                return [resp.status_code, resp.text]

    def put(self, url, data, headers):
        '''Encapsulate put API.'''
        allure.attach(url, name="Request URL")
        allure.attach(str(data), name="Request Body")

        resp = requests.put(url, json=data, headers=headers, verify=False, allow_redirects=False)
        # print(url)
        # print(url, resp.status_code, resp.text)

        allure.attach(str(resp.status_code), name="Response Code")
        allure.attach(resp.text, name="Response Result")

        if resp.text == "":
            return [resp.status_code, resp.text]
        else:
            try:
                return [resp.status_code, resp.json()]
            except:
                # If the response is not valid JSON, return the original text.
                return [resp.status_code, resp.text]

    def delete(self, url, data, headers):
        '''Encapsulate delete API.'''
        allure.attach(url, name="Request URL")
        allure.attach(str(data), name="Request Body")

        resp = requests.delete(url, json=data, headers=headers, verify=False, allow_redirects=False)
        # print(resp.status_code,resp.text)

        allure.attach(str(resp.status_code), name="Response Code")
        allure.attach(resp.text, name="Response Result")

        if resp.text == "":
            return [resp.status_code, resp.text]
        else:
            try:
                return [resp.status_code, resp.json()]
            except:
                # If the response is not valid JSON, return the original text.
                return [resp.status_code, resp.text]

    def upload_file(self, url, files, data, headers):
        '''Encapsulated file upload API.'''
        allure.attach(url, name="Request URL")
        allure.attach(str(data), name="Request Data")

        resp = requests.post(url, files=files, data=data, headers=headers, verify=False, allow_redirects=False)

        allure.attach(str(resp.status_code), name="Response Code")
        allure.attach(resp.text, name="Response Result")

        if resp.text == "":
            return [resp.status_code, resp.text]
        else:
            try:
                return [resp.status_code, resp.json()]
            except:
                # If the response is not valid JSON, return the original text.
                return [resp.status_code, resp.text]

    def post_with_timeout(self, url, data, headers, timeout):
        '''Encapsulate the post API with timeout.'''
        allure.attach(url, name="Request URL")
        allure.attach(str(data), name="Request Body")
        allure.attach(f"Timeout: {timeout}s", name="Request Timeout")

        resp = requests.post(url, json=data, headers=headers, verify=False, allow_redirects=False, timeout=timeout)

        allure.attach(str(resp.status_code), name="Response Code")
        allure.attach(resp.text, name="Response Result")

        if resp.text == "":
            return [resp.status_code, resp.text]
        else:
            try:
                return [resp.status_code, resp.json()]
            except:
                # If the response is not valid JSON, return the original text.
                return [resp.status_code, resp.text]

    def post_with_retry(self, url, data, headers, timeout=60, max_retries=2, retry_status_codes=[500, 502, 503, 504]):
        '''
        Encapsulate the post API with timeout and retry.
        :param url: request URL.
        :param data: request data.
        :param headers: request headers.
        :param timeout: timeout time (seconds), default 60 seconds.
        :param max_retries: Maximum number of retries, default 2 times.
        :param retry_status_codes: List of status codes that need to be retried, default [500, 502, 503, 504]
        :return: (status_code, response_data)
        '''
        import time
        
        allure.attach(url, name="Request URL")
        allure.attach(str(data), name="Request Body")
        allure.attach(f"Timeout: {timeout}s, Max Retries: {max_retries}", name="Request Config")
        
        last_exception = None
        last_status_code = None
        last_response = None
        
        for attempt in range(max_retries + 1):
            try:
                if attempt > 0:
                    # Wait before retrying, using exponential backoff.
                    wait_time = min(2 ** attempt, 10)  # Wait up to 10 seconds.
                    print(f"重试第 {attempt} 次，等待 {wait_time} 秒后重试...")
                    time.sleep(wait_time)
                    allure.attach(f"Retry attempt {attempt}", name="Retry Info")
                
                resp = requests.post(url, json=data, headers=headers, verify=False, allow_redirects=False, timeout=timeout)
                
                allure.attach(str(resp.status_code), name="Response Code")
                allure.attach(resp.text, name="Response Result")
                
                # If the status code is not in the retry list, return directly.
                if resp.status_code not in retry_status_codes:
                    if resp.text == "":
                        return [resp.status_code, resp.text]
                    else:
                        try:
                            return [resp.status_code, resp.json()]
                        except:
                            return [resp.status_code, resp.text]
                
                # If status code requires retry, log and continue.
                last_status_code = resp.status_code
                if resp.text == "":
                    last_response = resp.text
                else:
                    try:
                        last_response = resp.json()
                    except:
                        last_response = resp.text
                
                print(f"请求返回状态码 {resp.status_code}，需要重试（尝试 {attempt + 1}/{max_retries + 1}）")
                
            except requests.exceptions.Timeout:
                last_exception = f"Timeout after {timeout}s"
                print(f"请求超时（尝试 {attempt + 1}/{max_retries + 1}）")
                if attempt < max_retries:
                    continue
                else:
                    return [504, {"error": f"Request timeout after {timeout}s", "retries": max_retries + 1}]
            except requests.exceptions.RequestException as e:
                last_exception = str(e)
                print(f"请求异常: {e}（尝试 {attempt + 1}/{max_retries + 1}）")
                if attempt < max_retries:
                    continue
                else:
                    return [500, {"error": str(e), "retries": max_retries + 1}]
        
        # All retries fail and the last result is returned.
        if last_status_code:
            return [last_status_code, last_response]
        else:
            return [500, {"error": last_exception or "Unknown error", "retries": max_retries + 1}]

    def get_with_retry(self, url, headers, timeout=60, max_retries=2, retry_status_codes=[500, 502, 503, 504]):
        '''
        Encapsulate the get API with timeout and retry.
        :param url: request URL.
        :param headers: request headers.
        :param timeout: timeout time (seconds), default 60 seconds.
        :param max_retries: Maximum number of retries, default 2 times.
        :param retry_status_codes: List of status codes that need to be retried, default [500, 502, 503, 504]
        :return: (status_code, response_data)
        '''
        import time
        
        allure.attach(url, name="Request URL")
        allure.attach(f"Timeout: {timeout}s, Max Retries: {max_retries}", name="Request Config")
        
        last_exception = None
        last_status_code = None
        last_response = None
        
        for attempt in range(max_retries + 1):
            try:
                if attempt > 0:
                    # Wait before retrying, using exponential backoff.
                    wait_time = min(2 ** attempt, 10)  # Wait up to 10 seconds.
                    print(f"重试第 {attempt} 次，等待 {wait_time} 秒后重试...")
                    time.sleep(wait_time)
                    allure.attach(f"Retry attempt {attempt}", name="Retry Info")
                
                resp = requests.get(url, headers=headers, verify=False, allow_redirects=False, timeout=timeout)
                
                allure.attach(str(resp.status_code), name="Response Code")
                allure.attach(resp.text, name="Response Result")
                
                # If the status code is not in the retry list, return directly.
                if resp.status_code not in retry_status_codes:
                    try:
                        return [resp.status_code, resp.json()]
                    except:
                        return [resp.status_code, resp.text]
                
                # If status code requires retry, log and continue.
                last_status_code = resp.status_code
                try:
                    last_response = resp.json()
                except:
                    last_response = resp.text
                
                print(f"请求返回状态码 {resp.status_code}，需要重试（尝试 {attempt + 1}/{max_retries + 1}）")
                
            except requests.exceptions.Timeout:
                last_exception = f"Timeout after {timeout}s"
                print(f"请求超时（尝试 {attempt + 1}/{max_retries + 1}）")
                if attempt < max_retries:
                    continue
                else:
                    return [504, {"error": f"Request timeout after {timeout}s", "retries": max_retries + 1}]
            except requests.exceptions.RequestException as e:
                last_exception = str(e)
                print(f"请求异常: {e}（尝试 {attempt + 1}/{max_retries + 1}）")
                if attempt < max_retries:
                    continue
                else:
                    return [500, {"error": str(e), "retries": max_retries + 1}]
        
        # All retries fail and the last result is returned.
        if last_status_code:
            return [last_status_code, last_response]
        else:
            return [500, {"error": last_exception or "Unknown error", "retries": max_retries + 1}]

    def pathdelete(self, url, headers):
        '''Encapsulate the delete API and pass parameters via path.'''
        allure.attach(url, name="Request URL")

        resp = requests.delete(url, headers=headers, verify=False, allow_redirects=False)
        # print(url)
        # print(resp.text)

        allure.attach(str(resp.status_code), name="Response Code")
        allure.attach(resp.text, name="Response Result")

        if resp.text == "":
            return [resp.status_code, resp.text]
        else:
            try:
                return [resp.status_code, resp.json()]
            except:
                # If the response is not valid JSON, return the original text.
                return [resp.status_code, resp.text]
