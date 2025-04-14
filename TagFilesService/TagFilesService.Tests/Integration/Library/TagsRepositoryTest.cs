using Microsoft.EntityFrameworkCore;
using TagFilesService.Infrastructure;
using TagFilesService.Model;

namespace TagFilesService.Tests.Integration.Library;

[TestClass]
public class TagsRepositoryTest : InMemoryDatabaseTestBase
{
    [TestMethod]
    public async Task SaveTag_ShouldAddNewTag_WhenTagIsNew()
    {
        TagsRepository repository = new(DbContext);
        Tag tag = new("NewTag");

        Tag savedTag = await repository.SaveTag(tag);
        Tag? tagFromDb = await DbContext.Tags.FindAsync(1u);

        Assert.AreEqual(1u, savedTag.Id);
        Assert.AreEqual("NewTag", savedTag.Name);

        Assert.AreEqual(1u, tagFromDb?.Id);
        Assert.AreEqual("NewTag", tagFromDb?.Name);
    }

    [TestMethod]
    public async Task SaveTag_ShouldUpdateExistingTag_WhenTagIsNotNew()
    {
        TagsRepository repository = new(DbContext);
        await repository.SaveTag(new("ExistingTag"));

        Tag? existingTag = await DbContext.Tags.FindAsync(1u);
        existingTag?.Update("NewName");
        Tag updatedTag = await repository.SaveTag(existingTag!);
        Tag? updatedTagFromDb = await DbContext.Tags.FindAsync(1u);

        Assert.AreEqual(1u, updatedTag.Id);
        Assert.AreEqual("NewName", updatedTag.Name);

        Assert.AreEqual(1u, updatedTagFromDb?.Id);
        Assert.AreEqual("NewName", updatedTagFromDb?.Name);
    }

    [TestMethod]
    public async Task GetTags_ShouldReturnAllTagsOrderedByName()
    {
        DbContext.Tags.Add(new("TagB"));
        DbContext.Tags.Add(new("999"));
        DbContext.Tags.Add(new("TagA"));
        DbContext.Tags.Add(new("123"));
        DbContext.Tags.Add(new("TagC"));
        await DbContext.SaveChangesAsync();

        TagsRepository repository = new(DbContext);
        List<Tag> tags = await repository.GetTags();

        Assert.AreEqual(5, tags.Count);
        Assert.AreEqual("123", tags[0].Name);
        Assert.AreEqual("999", tags[1].Name);
        Assert.AreEqual("TagA", tags[2].Name);
        Assert.AreEqual("TagB", tags[3].Name);
        Assert.AreEqual("TagC", tags[4].Name);
    }

    [TestMethod]
    public async Task DeleteTag_ShouldRemoveTag_WhenTagExists()
    {
        DbContext.Tags.Add(new("tag1"));
        DbContext.Tags.Add(new("tag1"));
        DbContext.Tags.Add(new("tag1"));
        await DbContext.SaveChangesAsync();

        TagsRepository repository = new(DbContext);
        await repository.DeleteTag(2u);
        Tag? deletedTag = await DbContext.Tags.FindAsync(2u);
        int count = await DbContext.Tags.CountAsync();

        Assert.IsNull(deletedTag);
        Assert.AreEqual(2, count);
    }
}