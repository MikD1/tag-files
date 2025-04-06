using Microsoft.EntityFrameworkCore;
using TagFilesService.Model;

namespace TagFilesService.Data;

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

    public async Task DeleteTag(uint id)
    {
        Tag? tag = await dbContext.Tags.FindAsync(id);
        if (tag is null)
        {
            throw new ApplicationException($"Tag {id} not found");
        }

        dbContext.Tags.Remove(tag);
        await dbContext.SaveChangesAsync();
    }
}