Milestone XProtect Events API Reference
-
-
-
-
-
-
- Introduction- What is an event?
- Integrating with rules and alarms
- Quickstart- Requirements
- cURL
- Python
- Python (Windows User)
- Node.js
- Powershell
- Filtering
- Ordering
- Paging
- Events API Reference- Events- getRetrieve an event
- getList all events
- postTrigger an event
- postTrigger multiple events
- EventSessions- getGet event lines based on a session id.
- postCreates a session to request for new or deleted from session events.
- delDeletes a session.
API docs by Redocly
Milestone XProtect RESTful Events API  ( 1.0.0 )
Download OpenAPI specification :Download
Introduction
Using the RESTful Events API, you can retrieve and trigger events in XProtect. To get started quickly with the Events REST API, refer to our comprehensive quickstart quide. For more detailed information about the Events REST API and its endpoints, refer to the complete API reference.
What is an event?
An event is any atomic occurrence that happens within an XProtect system. An event is associated to one event type and one source.
Source
The event source is the item where the event originated. For example, a camera. You can list all cameras with the Configuration REST API: Cameras.
In the case of user defined events, the user defined event is used as both the type and the source when there is no related source.
Authorization
An API request with a service token can access all sources. In contrast, a request with a user token can only access the sources that the user is authorized for.
💡 A service token is a token that is not associated to a user; service tokens have super administrator access to all the system. In contrast, a user token is a token that is associated to a specific user; user tokens are limited to the authorization grants of the specific user.
Event types
Motion detection, License plate recognition or Fall detection are examples of event types. You can list available event types or create new event types with the Configuration REST API: Event types.
Authorization
An API request with a service token can trigger events of any event type in GET /eventTypes. In contrast, a request with a user token can only trigger events with generatorType=External (User Defined Events) and generatorType=MIPDevice (Analytics Events and other event types defined by plugins), excluding those with generatorSubtype=Generic (Generic Events).
Integrating with rules and alarms
An event can trigger rules and alarms.
Rules
You can create a Rule with the Configuration REST API: Rules that is triggered for a specific event type.
If you trigger an event with the Events REST API, the rule will be triggered if the event type matches it.
Alarms
You can create an Alarm Definition with the Configuration REST API: Alarm definitions for a specific event type.
If you trigger an event with the Events REST API, a new alarm will be triggered if the event type matches the Alarm definition.
Quickstart
Requirements
To use the Events REST API, you must have:
- A system running XProtect 2023 R2 or later. You are recommended to set up a small separate system for development. If you don't have one, install XProtect following the Get started guide.
- A Basic user or Windows user with access to some devices. If you don't have one, go to Create basic users.
⚠️ User credentials, bearer tokens, and other sensitive data are transmitted in cleartext if you do not set up certificates and use HTTPS. Make sure that your XProtect system is set up with HTTPS in production.
cURL
Follow this quickstart guide to retrieve events with cURL.
- Verify the requirements
Check the initial Requirements.
- Get an auth token
Replace the hostname test-01.example.com, username myUser, and password myPassword before running the following sample.
curl --insecure --request POST "https://test-01.example.com/API/IDP/connect/token" \
--header "Content-Type: application/x-www-form-urlencoded" \
--data-urlencode "grant_type=password" \
--data-urlencode "username=myUsername" \
--data-urlencode "password=myPassword" \
--data-urlencode "client_id=GrantValidatorClient"
In Windows Command Prompt (CMD), replace the line continuation character \ with ^.
- Retrieve events
Replace the hostname test-01.example.com and the sample token eyJhbGciOiJodHRwOi8vd3d3LnczLm9yZy8yMDAxLzA0L... ay0ferTwm-DZ4OxNXGTHk5t7R_YTWPjg before running the following sample.
curl --insecure --request GET "https://test-01.example.com/api/rest/v1/events" \
--header "Authorization: Bearer eyJhbGciOiJodHRwOi8vd3d3LnczLm9yZy8yMDAxLzA0L... ay0ferTwm-DZ4OxNXGTHk5t7R_YTWPjg"
In Windows Command Prompt (CMD), replace the line continuation character \ with ^.
☑️ If you receive an empty array, make sure that the events you are triggering are being stored. In Management Client, select Tools > Options > Alarms and Events and set Retention time to at least 1 day.
Python
Follow this quickstart guide to retrieve events with Python.
- Verify the requirements
Check the initial Requirements.
- Install dependencies
pip install requests
- Retrieve events
Replace the USERNAME username, PASSWORD password, and SERVER_URL https://test-01.example.com before running the following sample.
import requests  # pip install requests
USERNAME = 'username'  # replace with your basic user name
PASSWORD = 'password'  # replace with your basic user password
SERVER_URL = 'https://test-01.example.com'  # replace with your server URL
VERIFY_CERTIFICATES = True  # set to False in development if your certificates are self-signed
def main():
session = requests.Session()
# 1. Retrieve a token from IDP
response = session.request("POST",
f"{SERVER_URL}/API/IDP/connect/token",
headers={
'Content-Type': 'application/x-www-form-urlencoded'
},
data=f"grant_type=password"
f"&username={USERNAME}"
f"&password={PASSWORD}"
f"&client_id=GrantValidatorClient",
verify=VERIFY_CERTIFICATES)
if response.status_code != 200:
return print(f"Unable to retrieve token: {response.status_code}")
token = response.json()['access_token']
# 2. Retrieve events with the token
response = session.request("GET",
f"{SERVER_URL}/api/rest/v1/events",
headers={
'Authorization': f'Bearer {token}'
},
verify=VERIFY_CERTIFICATES)
if response.status_code != 200:
return print(f"Unable to retrieve events: {response.status_code}")
events = response.json()
print(f"GET /events response:\n{events}\n\n")
if __name__ == '__main__':
main()
☑️ If you receive an empty array, make sure that the events you are triggering are being stored. In Management Client, select Tools > Options > Alarms and Events and set Retention time to at least 1 day.
Python (Windows User)
Follow this quickstart guide to retrieve events with Python using a Windows users.
- Verify the requirements
Check the initial Requirements.
- Install dependencies
pip install requests requests-ntlm
- Retrieve events
Replace the USERNAME username, PASSWORD password, and SERVER_URL https://test-01.example.com before running the following sample.
import requests  # pip install requests
from requests_ntlm import HttpNtlmAuth  # pip install requests-ntlm
USERNAME = 'domain\\username'  # replace with your windows domain and username
PASSWORD = 'password'  # replace with your windows user password
SERVER_URL = 'https://test-01.example.com'  # replace with your server URL
VERIFY_CERTIFICATES = True  # set to False in development if your certificates are self-signed
def main():
session = requests.Session()
# 1. Retrieve a token from IDP
response = session.request("POST",
f"{SERVER_URL}/API/IDP/connect/token",
headers={
'Content-Type': 'application/x-www-form-urlencoded'
},
data="grant_type=windows_credentials&client_id=GrantValidatorClient",
verify=VERIFY_CERTIFICATES,
auth=HttpNtlmAuth(USERNAME, PASSWORD))
if response.status_code != 200:
return print(f"Unable to retrieve token: {response.status_code}")
token = response.json()['access_token']
# 2. Retrieve events with the token
response = session.request("GET",
f"{SERVER_URL}/api/rest/v1/events",
headers={
'Authorization': f'Bearer {token}'
},
verify=VERIFY_CERTIFICATES)
if response.status_code != 200:
return print(f"Unable to retrieve events: {response.status_code}")
events = response.json()
print(f"GET /events response:\n{events}\n\n")
if __name__ == '__main__':
main()
☑️ If you receive an empty array, make sure that the events you are triggering are being stored. In Management Client, select Tools > Options > Alarms and Events and set Retention time to at least 1 day.
Node.js
Follow this quickstart guide to retrieve events with Node.js.
- Verify the requirements
Check the initial Requirements.
- Retrieve events
Replace the USERNAME username, PASSWORD password, and SERVER_URL https://test-01.example.com before running the following sample.
const USERNAME = 'username';  // replace with your basic user name
const PASSWORD = 'password';  // replace with your basic user password
const SERVER_URL = 'https://test-01.example.com';  // replace with your server URL
const VERIFY_CERTIFICATES = true;  // set to False in development if your certificates are self-signed
if (!VERIFY_CERTIFICATES) process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
fetch(`${SERVER_URL}/API/IDP/connect/token`, {
method: 'POST',
headers:{
'Content-Type': 'application/x-www-form-urlencoded'
},
body: new URLSearchParams({
grant_type: 'password',
username: USERNAME,
password: PASSWORD,
client_id: 'GrantValidatorClient'
})
})
.then(response => response.json())
.then(token_response => {
const token = token_response['access_token'];
return fetch(`${SERVER_URL}/api/rest/v1/events`, {
headers:{
'Authorization': `Bearer ${token}`
}
})
})
.then(response => response.json())
.then(events => console.log(events))
.catch((err) => {
console.log(err);
});
☑️ If you receive an empty array, make sure that the events you are triggering are being stored. In Management Client, select Tools > Options > Alarms and Events and set Retention time to at least 1 day.
Powershell
Follow this quickstart guide to retrieve events with the PowerShell.
- Verify the requirements
Check the initial Requirements.
- Get an auth token
Replace the hostname test-01.example.com, username myUser, and password myPassword before running the following sample.
$headers = @{ "Content-Type" = "application/x-www-form-urlencoded" }
$body = @{grant_type='password'
username='myUsername'
password='myPassword'
client_id='GrantValidatorClient'}
$response = Invoke-RestMethod 'https://test-01.example.com/API/IDP/connect/token' -Method 'POST' -Headers $headers -Body $body
$response | ConvertTo-Json
- Retrieve events
Replace the hostname test-01.example.com and the sample token eyJhbGciOiJodHRwOi8vd3d3LnczLm9yZy8yMDAxLzA0L... ay0ferTwm-DZ4OxNXGTHk5t7R_YTWPjg before running the following sample.
$headers = @{ "Authorization" = "Bearer eyJhbGciOiJodHRwOi8vd3d3LnczLm9yZy8yMDAxLzA0L...ay0ferTwm-DZ4OxNXGTHk5t7R_YTWPjg" }
$response = Invoke-RestMethod 'https://test-01.example.com/api/rest/v1/events' -Method 'GET' -Headers $headers
$response | ConvertTo-Json
☑️ If you receive an empty array, make sure that the events you are triggering are being stored. In Management Client, select Tools > Options > Alarms and Events and set Retention time to at least 1 day.
Filtering
You can add match filters to a resource URI to limit the amount of data returned.
GET /resource?property=operator:value
string and DateTime values are delimited with single quotation marks ('), but int values are not.
GET /resource?property=lt:'2023-04-14T07:42:49.223Z'
GET /resource?property=contains:'John'
GET /resource?property=gt:123
Array values are required by some operators like oneOf. Arrays are comma-separated list of values delimited by ( and ):
GET /resource?property=oneOf:(1,4,8)
GET /resource?property=oneOf:('string value 1','string value 2')
When filtering by string values, escape any single quote ' as two single quotes ''.
GET /resource?property=contains:'string with ''single'' quotes'
GET /resource?property=oneOf:('with ''quote''', 'more ''quotes''')
When no filter operator is included, the operator defaults to equals.
GET /resource?property=value
GET /resource?property=equals:value
You can use any of the other available operators, like this:
GET /resource?property=notEquals:123
GET /resource?property=gt:123
GET /resource?property=lt:'2023-04-14T07:42:49.223Z'
GET /resource?property=startsWith:'Event'
GET /resource?property=contains:'a substring'
GET /resource?property=oneOf:(1,2,3)
You can combine multiple operators as a comma separated array, like this:
GET /resource?property=lt:'2023-04-14T07:42:49.223Z',gt:'2023-04-12T07:42:49.223Z'
Available filter operators:
- lt less than
- gt greater than
- equals equals
- notEquals not equals
- startsWith string starts with
- contains string that contains
- oneOf equals one of the values in the array
Ordering
You can use the orderBy query parameter to order the result of some resource collections.
GET /resource?orderBy=asc:'fieldName'
GET /resource?orderBy=desc:'fieldName'
You can fine tune the ordering combining multiple statements as a comma separated array, like this:
GET /resource?orderBy=asc:'field1',desc:'field2',asc:'field3'
Available ordering operators:
- asc ascending
- desc descending
Paging
You can use the page query parameter to specify the page number to retrieved from the results. The value must be at least 0.
You can use the size query parameter to specify the number of items per page. The value must be between 1 and 2000
GET /resource?page=0&size=10
Events
Retrieve an event
Retrieves the details of an event given its id.
The list of events retrieved depends on the authorization provided by the token. If the token doesn't grant access to the source of the event, it's not possible to get that event.
Authorizations:
Bearer
path  Parameters
idrequired
string  < guid >
Event identifier as Guid.
query  Parameters
includestring or null  < include >
Responses
200  Found an event for the given id.
400  Bad request.
401  Missing or invalid authentication token.
403  Calling user lacks required permissions or roles.
404  No event matches the given id, or it is not stored because of the event type retention policy.
get/events/{id}
/events/{id}
Response samples
- 200
Content typeapplication/json
Copy
Expand all  Collapse all
{- "data": {- "specversion": "1.0‎",
- "type": "481d4d6c-9156-4698-af7e-74b32f9e4359",
- "source": "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
- "time": "2023-02-28T18:10:12.4786334Z",
- "id": "5e6a4c06-b290-473d-80b5-8e0dffa1d3a6",
- "localId": 4,
- "datatype": "none",
- "tag": "My custom tag"
}
}
List all events
List past stored events starting from the oldest. If an event is not stored because of it's event type retention policy, it is not returned.
The list of events retrieved depends on the authorization provided by the token. If the token doesn't grant access to the source of the event, it's not possible to get that event.
Authorizations:
Bearer
query  Parameters
orderBystring or null  < orderBy >
Example:   orderBy=asc:&#x27;time&#x27;
Order events according to time, tag or source.name fields
Allowed operators: asc, desc (see ordering).
Allowed fields: time,tag,source.name
timestring or null  < filter >
Example:   time=gt:&#x27;2023-03-01T12:02:55Z&#x27;,lt:&#x27;2023-03-01T14:02:55.18Z&#x27;
Filter events according to the time when they happened in ISO8601 (UTC). Supports an accuracy of 10ms.
Allowed operators: gt and lt (see filtering).
source.namestring or null  < filter >
Example:   source.name=notEquals:&#x27;Camera main entry&#x27;
Filter events according to the source name.
Allowed operators: equals, notEquals, contains, startsWith, oneOf, containsOneOf (see filtering).
messagestring or null  < filter >
Example:   message=equals:&#x27;Smart Wall changed&#x27;
Filter events according to the event message.
Allowed operators: equals, notEquals, contains, startsWith, oneOf, containsOneOf (see filtering).
source.idstring or null  < filter >
Example:   source.id=equals:&#x27;cameras/d0ba5376-35b6-4cb6-9d38-a976287f3569&#x27;
Filter events according to the source id. Supports both formats: id and REST resource uri.
Allowed operators: equals, notEquals, oneOf (see filtering).
typestring or null  < filter >
Example:   type=equals:&#x27;d0ba5376-35b6-4cb6-9d38-a976287f3569&#x27;
Filter events according to the type.
Allowed operators: equals, notEquals, oneOf (see filtering).
type.namestring or null  < filter >
Example:   type.name=equals:&#x27;External event&#x27;
Filter events according to the type name.
Allowed operators: equals, notEquals, oneOf (see filtering).
typeGroupstring or null  < filter >
Example:   typeGroup=equals:&#x27;d0ba5376-35b6-4cb6-9d38-a976287f3569&#x27;
Filter events according to the type group id.
Allowed operators: equals, notEquals, oneOf (see filtering).
tagstring or null  < filter >
Example:   tag=notEquals:&#x27;2451GHM&#x27;
Filter events according to custom tag.
Allowed operators: equals, notEquals, contains, startsWith, oneOf, containsOneOf (see filtering).
analyticData.object.datastring or null  < filter >
Example:   analyticData.object.data=contains:&#x27;F_standard_since_2004_1&#x27;
Filter events according to analytic object data.
Allowed operators: equals, notEquals, contains, startsWith, oneOf, containsOneOf (see filtering).
analyticData.object.valuestring or null  < filter >
Example:   analyticData.object.value=notEquals:&#x27;2451GHM&#x27;
Filter events according to analytic object value.
Allowed operators: equals, notEquals, contains, startsWith, oneOf, containsOneOf (see filtering).
analyticData.rule.namestring or null  < filter >
Example:   analyticData.rule.name=notEquals:&#x27;2451GHM&#x27;
Filter events according to analytic data rule name.
Allowed operators: contains, containsOneOf` (see filtering).
analyticData.object.vendorstring or null  < filter >
Example:   analyticData.object.vendor=notEquals:&#x27;axis&#x27;
Filter events according to analytic data vendor name.
Allowed operators: equals, notEquals, contains, startsWith, oneOf, containsOneOf (see filtering).
localIdstring or null  < filter >
Example:   localId=equals:1
Filter events according to the local id
Allowed operators: equals, notEquals, oneOf (see filtering).
includestring or null  < include >
pageinteger  < int32 >
sizeinteger  < int32 >
Responses
200  Found events.
400  Bad request.
401  Missing or invalid authentication token.
403  Calling user lacks required permissions or roles.
get/events
/events
Response samples
- 200
Content typeapplication/json
Copy
Expand all  Collapse all
{- "_links": {- "prev": "string",
- "next": "string"
},
- "array": [- {- "specversion": "1.0‎",
- "type": "481d4d6c-9156-4698-af7e-74b32f9e4359",
- "source": "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
- "time": "2023-02-28T18:10:12.4786334Z",
- "id": "5e6a4c06-b290-473d-80b5-8e0dffa1d3a6",
- "localId": 4,
- "datatype": "none",
- "tag": "My custom tag"
}
],
- "eventCount": 0
}
Trigger an event
Create and trigger a new event. The event will be processed and trigger alarms and rules that are set up.
Authorizations:
Bearer
query  Parameters
triggerRulesboolean
Example:   triggerRules=true
Trigger rules (true by default). This attribute is legacy and will be removed without notice.
Request Body schema: application/jsonrequired
typerequired
string  < guid >
Type of the event.
Retrieve valid event types from the Configuration API.
sourcestring or null
Source that triggered the event.
Some events (i.e. User Defined Events) can have the source set to null.
The source must be a valid REST resource string or a guid.
If the source device is camera the format can be cameras/{id} or simply {id}
Retrieve valid cameras from the Configuration API.
timestring or null  < date-time >
Time at which the event was triggered in ISO 8601 (UTC) format.
If not given, the time will be automatically filled when the server processes the request.
datatyperequired
string or null
Specifies the type of data that is included with the event.
User defined events do not support data and datatype.
analyticsfree-formnoneanalytics
tagstring or null
Custom tag assignable to the event (for filtering and sorting purposes)
data(AnalyticsData (object or null))
Event metadata.
Responses
202  The event has been triggered.
400  Bad request.
401  Missing or invalid authentication token.
403  Unauthorized to trigger the event type or unauthorized to access the source.
post/events
/events
Request samples
- Payload
Content typeapplication/json
Exampleanalyticsfree-formnoneanalytics
Copy
Expand all  Collapse all
{- "type": "481d4d6c-9156-4698-af7e-74b32f9e4359",
- "source": "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
- "time": "2009-02-15T00:00:00.0000000Z",
- "datatype": "analytics",
- "tag": "My custom tag",
- "data": { }
}
Response samples
- 202
Content typeapplication/json
Copy
Expand all  Collapse all
{- "data": {- "specversion": "1.0‎",
- "type": "481d4d6c-9156-4698-af7e-74b32f9e4359",
- "source": "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
- "time": "2023-02-28T18:10:12.4786334Z",
- "id": "5e6a4c06-b290-473d-80b5-8e0dffa1d3a6",
- "localId": 4,
- "datatype": "none",
- "tag": "My custom tag"
}
}
Trigger multiple events
Create and trigger multiple events. The events will be processed and trigger alarms and rules that are set up.
Authorizations:
Bearer
query  Parameters
triggerRulesboolean
Example:   triggerRules=true
Trigger rules (true by default). This attribute is legacy and will be removed without notice.
Request Body schema: application/jsonrequired
Array
typerequired
string  < guid >
Type of the event.
Retrieve valid event types from the Configuration API.
sourcestring or null
Source that triggered the event.
Some events (i.e. User Defined Events) can have the source set to null.
The source must be a valid REST resource string or a guid.
If the source device is camera the format can be cameras/{id} or simply {id}
Retrieve valid cameras from the Configuration API.
timestring or null  < date-time >
Time at which the event was triggered in ISO 8601 (UTC) format.
If not given, the time will be automatically filled when the server processes the request.
datatyperequired
string or null
Specifies the type of data that is included with the event.
User defined events do not support data and datatype.
analyticsfree-formnoneanalytics
tagstring or null
Custom tag assignable to the event (for filtering and sorting purposes)
data(AnalyticsData (object or null))
Event metadata.
Responses
207  The request has been processed. The respose body contains the status code for each event
400  Bad request.
401  Missing or invalid authentication token.
post/events/bulk
/events/bulk
Request samples
- Payload
Content typeapplication/json
Copy
Expand all  Collapse all
[- {- "type": "481d4d6c-9156-4698-af7e-74b32f9e4359",
- "source": "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
- "time": "2009-02-15T00:00:00.0000000Z",
- "datatype": "none",
- "tag": "My custom tag"
}
]
Response samples
- 207
Content typeapplication/json
Copy
Expand all  Collapse all
{- "array": [- {- "data": {- "specversion": "1.0‎",
- "type": "481d4d6c-9156-4698-af7e-74b32f9e4359",
- "source": "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
- "time": "2023-02-28T18:10:12.4786334Z",
- "id": "5e6a4c06-b290-473d-80b5-8e0dffa1d3a6",
- "localId": 4,
- "datatype": "none",
- "tag": "My custom tag"
},
- "status": "202, 400",
- "property1": null,
- "property2": null
}
]
}
EventSessions
Get event lines based on a session id.
Returns  an array of new events and an array of events deleted from current version.
Authorizations:
Bearer
path  Parameters
idrequired
string  < guid >
query  Parameters
maxSizeinteger  < maxSize >
Example:   maxSize=data
Use the maxSize query parameter to define the number of requested events.
includestring or null  < include >
Example:   include=data
Use the include query parameter to receive optional fields.
Responses
200  The session has been created.
400  Bad request.
401  Missing or invalid authentication token.
403  The user has no permission over the session.
404  Session not found.
get/eventSessions/{id}/events
/eventSessions/{id}/events
Response samples
- 200
Content typeapplication/json
Copy
Expand all  Collapse all
{- "data": {- "deletedFromSession": [- "string"
],
- "addedToSession": [- {- "specversion": "1.0‎",
- "type": "481d4d6c-9156-4698-af7e-74b32f9e4359",
- "source": "cameras/e028f597-c64b-4f27-bb8d-08f5cd768e1c",
- "time": "2023-02-28T18:10:12.4786334Z",
- "id": "5e6a4c06-b290-473d-80b5-8e0dffa1d3a6",
- "localId": 4,
- "datatype": "none",
- "tag": "My custom tag"
}
]
}
}
Creates a session to request for new or deleted from session events.
Create an event session with a set of filters and an ordering criteria.If the filters or the ordering changes, you must delete the current session and start a new one.Use the session id to retrieve the events in the session.
Authorizations:
Bearer
Request Body schema: application/jsonrequired
filterByrequired
Array of objects ( EventSessionFilter )
Filter conditions for events in the current session.
orderByrequired
Array of objects ( EventSessionOrderBy )
Order by conditions for events in the current session.
Responses
200  The session id.
400  Bad request.
401  Missing or invalid authentication token.
403  The user has no permission over events.
post/eventSessions
/eventSessions
Request samples
- Payload
Content typeapplication/json
Copy
Expand all  Collapse all
{- "filterBy": [- {- "operator": "equals",
- "target": "id",
- "value": null
}
],
- "orderBy": [- {- "target": "id",
- "direction": "asc"
}
]
}
Response samples
- 200
Content typeapplication/json
Copy
Expand all  Collapse all
{- "filterBy": [- {- "operator": "equals",
- "target": "id",
- "value": null
}
],
- "orderBy": [- {- "target": "id",
- "direction": "asc"
}
],
- "id": "string"
}
Deletes a session.
Authorizations:
Bearer
path  Parameters
idrequired
string  < guid >
The session id.
Responses
200  The session id.
400  Bad request.
401  Missing or invalid authentication token.
403  The user has no permission to execute this action.
404  Session not found.
delete/eventSessions/{id}
/eventSessions/{id}
Response samples
- 200
Content typeapplication/json
Copy
{ }

