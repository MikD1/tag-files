using Microsoft.EntityFrameworkCore;
using TagFilesService.Model;

namespace TagFilesService.Infrastructure;

public class TagsRepository(AppDbContext dbContext) : ITagsRepository
{
    public async Task<Tag> SaveTag(Tag tag)
    {
        if (tag.Id == 0)
        {
            dbContext.Tags.Add(tag);
        }
        else
        {
            dbContext.Tags.Update(tag);
        }

        await dbContext.SaveChangesAsync();
        return tag;
    }

    public async Task<List<Tag>> GetTags()
    {
        return await dbContext.Tags
            .OrderBy(x => x.Name)
            .ToListAsync();
    }

    public async Task<Tag> GetTag(string name)
    {
        return await GetTagByNameOrThrow(name);
    }

    public async Task DeleteTag(string name)
    {
        Tag tag = await GetTagByNameOrThrow(name);
        dbContext.Tags.Remove(tag);
        await dbContext.SaveChangesAsync();
    }

    private async Task<Tag> GetTagByNameOrThrow(string name)
    {
        Tag? tag = await dbContext.Tags.FirstOrDefaultAsync(x => x.Name == name);
        if (tag is null)
        {
            throw new ApplicationException($"Tag {name} not found");
        }

        return tag;
    }
}