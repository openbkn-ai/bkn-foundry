// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

grammar SqlBase;

options { caseInsensitive = true; }

statement: selectQuery SEMICOLON? EOF;

selectQuery
    : SELECT DISTINCT? selectItem (COMMA selectItem)* FROM relation
      (WHERE expression)? (GROUP BY expressionList)? (HAVING expression)?
      (ORDER BY sortItem (COMMA sortItem)*)? limitClause?
    ;

selectItem: (identifier DOT)? STAR | expression (AS? identifier)?;

relation: relationPrimary joinPart*;

relationPrimary: RESOURCE (AS? identifier)?
               | LPAREN selectQuery RPAREN AS? identifier
               | LPAREN relation RPAREN;

joinPart: ((INNER | LEFT OUTER? | RIGHT OUTER?)? JOIN relationPrimary ON expression)
        | CROSS JOIN relationPrimary;

sortItem: expression (ASC | DESC)?;

limitClause: LIMIT NUMBER (OFFSET NUMBER | COMMA NUMBER)?;

expressionList: expression (COMMA expression)*;

expression: orExpression;

orExpression: andExpression (OR andExpression)*;

andExpression: notExpression (AND notExpression)*;

notExpression: NOT notExpression | predicate;

predicate
    : additiveExpression
      ((EQ | NEQ | LT | LTE | GT | GTE) additiveExpression
      | IS NOT? NULL
      | NOT? BETWEEN additiveExpression AND additiveExpression
      | NOT? IN LPAREN expressionList RPAREN
      | NOT? LIKE additiveExpression (ESCAPE STRING)?)?
    ;

additiveExpression: multiplicativeExpression ((PLUS | MINUS) multiplicativeExpression)*;

multiplicativeExpression: unaryExpression ((STAR | SLASH | PERCENT) unaryExpression)*;

unaryExpression: (PLUS | MINUS) unaryExpression | primaryExpression;

primaryExpression
    : literal
    | identifier (DOT identifier)?
    | functionCall
    | CURRENT_TIMESTAMP (LPAREN RPAREN)?
    | CAST LPAREN expression AS typeName RPAREN
    | CASE expression? (WHEN expression THEN expression)+ (ELSE expression)? END
    | LPAREN expression RPAREN
    ;

functionCall: identifier LPAREN DISTINCT? (STAR | expressionList)? RPAREN;

typeName: identifier (LPAREN NUMBER (COMMA NUMBER)? RPAREN)?;

literal: NUMBER | STRING | NULL | TRUE | FALSE;

identifier: IDENTIFIER | QUOTED_IDENTIFIER;

SELECT: 'SELECT';
DISTINCT: 'DISTINCT';
FROM: 'FROM';
WHERE: 'WHERE';
GROUP: 'GROUP';
BY: 'BY';
HAVING: 'HAVING';
ORDER: 'ORDER';
LIMIT: 'LIMIT';
OFFSET: 'OFFSET';
AS: 'AS';
INNER: 'INNER';
LEFT: 'LEFT';
RIGHT: 'RIGHT';
OUTER: 'OUTER';
JOIN: 'JOIN';
CROSS: 'CROSS';
ON: 'ON';
ASC: 'ASC';
DESC: 'DESC';
OR: 'OR';
AND: 'AND';
NOT: 'NOT';
IS: 'IS';
NULL: 'NULL';
BETWEEN: 'BETWEEN';
IN: 'IN';
LIKE: 'LIKE';
ESCAPE: 'ESCAPE';
CAST: 'CAST';
CASE: 'CASE';
WHEN: 'WHEN';
THEN: 'THEN';
ELSE: 'ELSE';
END: 'END';
TRUE: 'TRUE';
FALSE: 'FALSE';
CURRENT_TIMESTAMP: 'CURRENT_TIMESTAMP';

// 已识别但未开放的语法，不能作为普通名称放行。
UNSUPPORTED: 'WITH' | 'UNION' | 'INTERSECT' | 'EXCEPT' | 'OVER' | 'WINDOW'
    | 'FULL' | 'NATURAL' | 'USING' | 'LATERAL' | 'FOR' | 'LOCK' | 'INTO'
    | 'INSERT' | 'UPDATE' | 'DELETE' | 'CREATE' | 'DROP' | 'ALTER' | 'SET'
    | 'EXISTS' | 'ROLLUP' | 'CUBE' | 'FETCH' | 'SQL_CALC_FOUND_ROWS';

RESOURCE options { caseInsensitive = false; }
    : '{{' [ \t\r\n]* [a-z0-9] [a-z0-9_-]* [ \t\r\n]* '}}';

LPAREN: '(';
RPAREN: ')';
COMMA: ',';
DOT: '.';
SEMICOLON: ';';
EQ: '=';
NEQ: '<>' | '!=';
LTE: '<=';
GTE: '>=';
LT: '<';
GT: '>';
PLUS: '+';
MINUS: '-';
STAR: '*';
SLASH: '/';
PERCENT: '%';

NUMBER: [0-9]+ ('.' [0-9]+)? ('E' [+-]? [0-9]+)?;
STRING: '\'' ('\'\'' | '\\' . | ~['\\])* '\''
      | '"' ('""' | '\\' . | ~["\\])* '"';

QUOTED_IDENTIFIER: '`' ('``' | ~'`')+ '`';
IDENTIFIER: [\p{L}_] [\p{L}\p{N}_$]*;

// 可执行注释和 hint 必须先于普通块注释识别。
FORBIDDEN_COMMENT: '/*' [!+] .*? '*/';

BLOCK_COMMENT: '/*' .*? '*/' -> channel(HIDDEN);
LINE_COMMENT: ('--' [ \t\r\n] | '#') ~[\r\n]* -> channel(HIDDEN);

WS: [ \t\r\n]+ -> channel(HIDDEN);
