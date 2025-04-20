using TagFilesService.Model;

namespace TagFilesService.Tests.Unit.Model;

[TestClass]
public class TagTest
{
    [DataTestMethod]
    [DataRow("tag", false)]
    [DataRow("123", false)]
    [DataRow("a", false)]
    [DataRow("1", false)]
    [DataRow("-", false)]
    [DataRow("---", false)]
    [DataRow("abc-123", false)]
    [DataRow("abc123", false)]
    [DataRow("", true)]
    [DataRow("123456789012345678901234567890123456789012345678901", true)]
    public void Tag_Constructor_ShouldCorrectValidateName(string name, bool expectedExceptionThrown)
    {
        bool isExceptionThrown = false;
        Tag? tag = null;
        try
        {
            tag = new(name);
        }
        catch (ApplicationException)
        {
            isExceptionThrown = true;
        }

        Assert.AreEqual(expectedExceptionThrown, isExceptionThrown);
        if (!expectedExceptionThrown)
        {
            Assert.AreEqual(name, tag?.Name);
        }
    }

    [TestMethod]
    public void Tag_Name_DoNotContainsSpecialCharacters()
    {
        const string specialCharacters = "!@#$%^&*()_+[]{}|;':\",.<>?/";
        foreach (char character in specialCharacters)
        {
            bool isExceptionThrown = false;
            try
            {
                // ReSharper disable once UnusedVariable
                Tag tag = new("tag" + character);
            }
            catch (ApplicationException)
            {
                isExceptionThrown = true;
            }

            Assert.IsTrue(isExceptionThrown);
        }
    }
}