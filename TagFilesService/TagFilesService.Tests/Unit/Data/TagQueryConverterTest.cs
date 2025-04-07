using TagFilesService.Infrastructure;

namespace TagFilesService.Tests.Unit.Data;

[TestClass]
public class TagQueryConverterTest
{
    [DataTestMethod]
    [DataRow("abc", "Tags.Any(Name==\"abc\")")]
    [DataRow("!tag-1", "!Tags.Any(Name==\"tag-1\")")]
    [DataRow("tag-1 && tag-2 && tag-3", "Tags.Any(Name==\"tag-1\")&&Tags.Any(Name==\"tag-2\")&&Tags.Any(Name==\"tag-3\")")]
    [DataRow("tag1 && (tag2 || tag3) && !tag4", "Tags.Any(Name==\"tag1\")&&(Tags.Any(Name==\"tag2\")||Tags.Any(Name==\"tag3\"))&&!Tags.Any(Name==\"tag4\")")]
    [DataRow("tag-1 && (tag-2 || tag-3) && !tag-4", "Tags.Any(Name==\"tag-1\")&&(Tags.Any(Name==\"tag-2\")||Tags.Any(Name==\"tag-3\"))&&!Tags.Any(Name==\"tag-4\")")]
    [DataRow("tag-1 && tag-2", "Tags.Any(Name==\"tag-1\")&&Tags.Any(Name==\"tag-2\")")]
    [DataRow("!tag-1 && tag-2", "!Tags.Any(Name==\"tag-1\")&&Tags.Any(Name==\"tag-2\")")]
    [DataRow("tag-1 || tag-2", "Tags.Any(Name==\"tag-1\")||Tags.Any(Name==\"tag-2\")")]
    [DataRow("tag-1 && (tag-2 || !tag-3)", "Tags.Any(Name==\"tag-1\")&&(Tags.Any(Name==\"tag-2\")||!Tags.Any(Name==\"tag-3\"))")]
    [DataRow("!tag-1 && !tag-2", "!Tags.Any(Name==\"tag-1\")&&!Tags.Any(Name==\"tag-2\")")]
    [DataRow("(tag-1 || tag-2) && tag-3", "(Tags.Any(Name==\"tag-1\")||Tags.Any(Name==\"tag-2\"))&&Tags.Any(Name==\"tag-3\")")]
    [DataRow("tag-1 && tag-2 && !tag-3", "Tags.Any(Name==\"tag-1\")&&Tags.Any(Name==\"tag-2\")&&!Tags.Any(Name==\"tag-3\")")]
    [DataRow("tag-5 && (tag-6 || tag-7) && !tag-8", "Tags.Any(Name==\"tag-5\")&&(Tags.Any(Name==\"tag-6\")||Tags.Any(Name==\"tag-7\"))&&!Tags.Any(Name==\"tag-8\")")]
    [DataRow("tag-10 && tag-11", "Tags.Any(Name==\"tag-10\")&&Tags.Any(Name==\"tag-11\")")]
    [DataRow("tagA && tagB && !tagC", "Tags.Any(Name==\"tagA\")&&Tags.Any(Name==\"tagB\")&&!Tags.Any(Name==\"tagC\")")]
    [DataRow("tagX || tagY", "Tags.Any(Name==\"tagX\")||Tags.Any(Name==\"tagY\")")]
    public void ConvertToDynamicQuery_ValidInput_ReturnsExpectedQuery(string input, string expected)
    {
        string result = TagQueryConverter.ConvertToDynamicQuery(input);

        Assert.AreEqual(expected, result);
    }
}