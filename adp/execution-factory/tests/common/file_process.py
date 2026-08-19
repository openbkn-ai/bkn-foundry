# -*- coding:UTF-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import json
import os
from pathlib import Path

class FileProcess():
    def write_json_to_file(self, data, filename, indent=4):
        """
        Write JSON data to file.
        
        Parameters:
            data: JSON data to be written (can be a dictionary, list, etc. serializable object)
            filename: the name of the file to be saved.
        """
        try:
             # Get the directory path where the file is located.
            dir_path = os.path.dirname(filename)
            
            # Create the directory (including any parent directories) if it does not exist.
            if dir_path and not os.path.exists(dir_path):
                # Use Path.mkdir() to create a directory. parents=True means creating all parent directories.
                # exist_ok=True means no error will be reported if the directory already exists.
                Path(dir_path).mkdir(parents=True, exist_ok=True)
                print(f"已创建目录: {dir_path}")
            # Use the with statement to open the file and ensure that the file is closed correctly after the operation is completed.
            # The indent parameter is used to format the output and make the JSON content more readable.
            with open(filename, 'w', encoding='utf-8') as file:
                json.dump(data, file, ensure_ascii=False, indent=4)
            print(f"JSON数据已成功写入文件: {filename}")
        except Exception as e:
            print(f"写入文件时发生错误: {e}")

# Example usage.
if __name__ == "__main__":
    # JSON data to write (in dictionary form)
    sample_data = {
        "name": "张三",
        "age": 30,
        "is_student": False,
        "hobbies": ["阅读", "旅行", "编程"],
        "address": {
            "city": "北京",
            "district": "海淀区"
        }
    }
    
    # Call a function to write data to a file.
    FileProcess().write_json_to_file(sample_data, "output.json")
