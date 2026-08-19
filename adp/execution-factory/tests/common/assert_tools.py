# -*- coding:UTF-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import json

class AssertTools:
    '''
    Verification tools.
    '''
    def is_descending_str(lst):
        """Determine whether the elements in the list are sorted in descending order."""
        if len(lst) <= 1:
            return True
        for i in range(len(lst) - 1):
            str1 = str(lst[i])
            str2 = str(lst[i + 1])
            # Compare characters bit by bit.
            for c1, c2 in zip(str1, str2):
                if ord(c1) < ord(c2):
                    return False  # Previous character < next character, descending order is not satisfied.
                elif ord(c1) > ord(c2):
                    break  # Previous character > Next character, satisfying descending order, continue to the next pair of strings.
            else:
                # If the loop is exited after traversing the short string, it means that the short string is the prefix of the long string.
                # Descending order requirement: short string must >= long string, so short string length cannot be longer.
                if len(str1) < len(str2):
                    return False
        return True

    def is_ascending_str(lst):
        """Determine whether the elements in the list are arranged in correct order."""
        if len(lst) <= 1:
            return True
        for i in range(len(lst) - 1):
            str1 = str(lst[i])
            str2 = str(lst[i + 1])
            # Compare characters bit by bit.
            for c1, c2 in zip(str1, str2):
                if ord(c1) > ord(c2):
                    return False
                elif ord(c1) < ord(c2):
                    break  # The current bit has satisfied the requirements of first small and last large, continue to compare the next string.
            else:
                # If the loop is exited after traversing the short string, it means that the short string is the prefix of the long string, and the short string is smaller.
                if len(str1) > len(str2):
                    return False
        return True
    
    def has_duplicates(lst):
        """Determine whether there are duplicate elements in the list."""
        return len(lst) != len(set(lst))
    
    def are_lists_equal(list1, list2):
        # JSON serialize each element and compare after sorting.
        sorted_list1 = sorted(json.dumps(item, sort_keys=True) for item in list1)
        sorted_list2 = sorted(json.dumps(item, sort_keys=True) for item in list2)
        return sorted_list1 == sorted_list2
